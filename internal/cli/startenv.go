package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// syncRepo is repos.Sync; tests replace it.
var syncRepo = repos.Sync

// StartOptions is everything StartEnv needs; the dashboard fills the same
// struct the CLI does, so both run one code path (design.md D6).
type StartOptions struct {
	Checkout        string
	NewStateBase    string
	LegacyStateBase string
	Global          []string // extra global flags (--npmrc, --secret) to forward
	ProfileDirs     []string
	Force           bool             // start a project without verified knowledge (CLI only)
	Mode            string           // container or hybrid; empty = the machine's configured mode
	Trigger         string           // who started it: cli (default) or dashboard; recorded in the run
	Reset           bool             // throw the environment away and start it from zero (command reset)
	Fingerprint     string           // FromMain: the confirmed recipe and move list; the run refuses when they changed
	FromMain        bool             // Reset: prepare with the main checkout's recipe, the worktree's own adapter files moved aside
	Current         bool             // Reset: go on with the current code when it is behind and cannot be updated
	Live            *live.Job        // a live reconcile: run only the steps of this job (command live, trigger live)
	Observer        func(runs.Event) // receives the run's state changes, may be nil
}

// startPlan is the plan of a checkout and the mode it starts in.
func startPlan(out io.Writer, o StartOptions, root, main string) (plan knowledge.Plan, mode string, err error) {
	kk, err := knowledge.LoadFirst(root, main)
	if err != nil { // a broken file is not an empty plan
		return plan, "", err
	}
	if kk != nil {
		plan = kk.Plan
	}
	mode = o.Mode
	if mode == "" {
		mode = knowledge.ModeContainer
		if cfg, err := config.Load(); err == nil {
			var note string
			if mode, note = cfg.ModeForPlan(main, plan); note != "" {
				fmt.Fprintln(out, "note: "+note)
			}
		}
	}
	return plan, mode, nil
}

// startRefusal is the readiness check of a Start: a checkout without a
// prepared instance is refused unless its project is ready (verified
// knowledge). With force, a project that only needs learn starts and warn
// says so. It touches nothing.
func startRefusal(root, main, branch, instanceDir string, insts []inventory.Instance, profileDirs []string, force bool) (warn string, err error) {
	if _, serr := os.Stat(filepath.Join(instanceDir, "manifest.json")); serr == nil {
		return "", nil
	}
	level, reason := repos.ReadinessOf(filepath.Base(main), append(repos.Checkouts(main), root), repos.DefaultBranchOf(main), insts, profileDirs)
	if level == repos.Ready {
		return "", nil
	}
	if !force || level != repos.NeedsLearn {
		return "", fmt.Errorf("%s is %s (%s); stackfork only starts projects whose startup was learned and verified. Ask your agent to learn it: `stackfork repos --prompt %s %s`",
			filepath.Base(main), level, reason, filepath.Base(main), branch)
	}
	return fmt.Sprintf("warning: %s is not verified (%s); starting anyway because of --force", filepath.Base(main), reason), nil
}

// StartEnv prepares (only when needed) and starts the instance of one
// checkout, writing progress lines to out. It refuses a project stackfork
// does not know how to run (design.md D3/D6).
func StartEnv(out, errOut io.Writer, o StartOptions) (retErr error) {
	if err := checkSecretFlags(o.Global); err != nil {
		return err
	}
	toplevel, err := gitToplevel(o.Checkout)
	if err != nil {
		return fmt.Errorf("%s is not a git checkout", o.Checkout)
	}
	root, err := identity.ResolvePath(toplevel)
	if err != nil {
		return err
	}
	branch, err := currentBranch(root)
	if err != nil {
		return err
	}
	main := repos.MainRepoOf(root)
	if main == "" {
		main = root
	}
	var mainP *mainPlan // reset --from-main: checked before anything runs or moves
	if o.FromMain {
		if !o.Reset {
			return errors.New("--from-main belongs to reset")
		}
		if mainP, err = mainRecipe(root, o.NewStateBase, o.LegacyStateBase); err != nil {
			return err
		}
		if o.Fingerprint != "" && o.Fingerprint != mainP.fp {
			return errors.New(errRecipeChanged)
		}
	}
	plan, mode, err := startPlan(out, o, root, main)
	if err != nil {
		return err
	}
	runner := lifecycle.PlanRunner{NewBase: o.NewStateBase, Main: main, Mode: mode, Plan: plan}
	if err := runner.Check(); err != nil { // before any Docker call
		return err
	}
	if o.Live != nil { // the full plan was checked; the run executes the steps of the job
		runner.Plan, runner.Exact = o.Live.Plan(plan), true
		runner.HostSkipped, runner.HostSkipReason = o.Live.HostSkipped(plan), live.HostSkipReason
	}
	insts := collectInstances(o.NewStateBase, o.LegacyStateBase)
	project, instanceDir, err := checkoutInstance(root, branch, o.NewStateBase, o.LegacyStateBase)
	if err != nil {
		return err
	}
	head := []string{"--state-dir", o.NewStateBase, "--legacy-state-dir", o.LegacyStateBase, "--root", root}
	base := append(slices.Clone(head), o.Global...)
	// The references a sibling worktree contributes, computed once: the
	// preflight and prepare both use this set (review I1).
	var sibling map[string]string // reset --from-main keeps only the instance's own references
	if !o.FromMain {
		if sibling, err = siblingRefs(out, o, root, main, instanceDir, insts); err != nil {
			return err
		}
	}

	warn, err := startRefusal(root, main, branch, instanceDir, insts, o.ProfileDirs, o.Force)
	if err != nil {
		return err
	}
	if warn != "" {
		fmt.Fprintln(errOut, warn)
	}

	trigger := o.Trigger
	if trigger == "" {
		trigger = "cli"
	}
	if o.Live != nil {
		trigger = live.TriggerLive
	}
	willPrepare := o.Reset || needsPrepare(instanceDir, root, branch, project)
	command, retry := "start-env", " start-env"
	if o.Reset {
		command, retry = "reset", " reset --yes"
		if o.FromMain {
			retry += " --from-main"
		}
	}
	meta := runs.Meta{Trigger: trigger, Mode: mode, Project: project, Branch: branch, Observer: o.Observer}
	if o.Live != nil { // a failed live run is retried by turning live mode on again (design D10)
		command, retry = "live", ""
		meta.NoWait, meta.Cause = true, &o.Live.Cause
	}
	if flags := flagSecrets(o.Global); !willPrepare && len(flags) > 0 {
		names := make([]string, 0, len(flags))
		for n := range flags {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintf(out, "note: --secret %s ignored: %s is already prepared; run `stackfork prepare --secret …` to change it.\n", strings.Join(names, ", "), project)
	}
	var vals lifecycle.SecretValues // the preflight's values redact every step after it (D11)
	secrets := func() []redact.Secret { return vals.Redactions() }
	meta.Command, meta.Out, meta.Secrets = command, out, secrets
	rec, err := runs.Begin(instanceDir, meta)
	if err != nil {
		return err
	}
	// Set now, not at the end: a killed process leaves an interrupted run
	// that the card can still retry. A failed seed replaces it.
	if o.Live != nil {
		rec.SetRetry("stackfork live on " + main)
	} else {
		rec.SetRetry("stackfork --root " + root + retry)
	}
	defer func() { // the run ends with the command, whatever path returned
		if p := recover(); p != nil { // a panic is a failed run, not a succeeded one
			rec.Finish(fmt.Errorf("panic: %v", p))
			panic(p)
		}
		rec.Finish(retErr)
	}()
	// sync: the checkout follows its upstream when that cannot lose anything;
	// otherwise it says how far behind it is. It never fails the start. The
	// plan already checked is the one that runs; a fast-forwarded plan applies
	// from the next start. A live run has no sync step: the reconciler fetched.
	if o.Live == nil {
		sync := rec.Step("sync", "sync", "fetch the upstream; fast-forward when clean")
		res := syncRepo(root, 15*time.Second, true)
		if res.Line != "" {
			fmt.Fprintln(sync.Start(), res.Line)
		} else {
			sync.Start()
		}
		if o.Reset && res.Blocked && !o.Current {
			err := errors.New("the checkout is behind its upstream and cannot be updated without touching your work; nothing was deleted. Reset with the current code: stackfork --root " + root + " reset --yes")
			sync.End(err, 0)
			return err
		}
		sync.End(nil, 0)
	}
	if o.Reset { // the code may have changed: its plan and mode apply to this run
		if plan, mode, err = startPlan(out, o, root, main); err != nil {
			return err
		}
		runner.Plan, runner.Mode = plan, mode
		if err := runner.Check(); err != nil {
			return err
		}
		rec.SetMode(mode)
	}

	// secrets: resolve every effective reference before prepare, which already
	// calls Docker, and fail naming every problem (design D8).
	if vals, err = startPreflight(rec, o, root, main, instanceDir, trigger, sibling, willPrepare); err != nil {
		return err
	}
	runValues.Store(inRunKey(instanceDir), vals)
	defer runValues.Delete(inRunKey(instanceDir))

	if o.Reset {
		if err := resetDown(rec, root, branch, project, instanceDir, o.NewStateBase, o.LegacyStateBase); err != nil {
			return err
		}
	}

	prepare := func(out io.Writer, _ []string) error {
		if !o.Reset && !needsPrepare(instanceDir, root, branch, project) {
			fmt.Fprintf(out, "%s is already prepared\n", project)
			return lifecycle.ErrAlreadyPrepared
		}
		var args []string
		var err error
		var seedCmd, seedSvc string
		fromSibling := false
		if mainP != nil { // inside prepare, so a failed sync, preflight or down moved nothing; no other recipe applies
			args, err = reuseMainRecipe(out, root, o)
		} else {
			if o.Reset {
				args, err = resetPrepareArgs(root, main, instanceDir)
				if fromSibling = errors.Is(err, errNoRecipe); fromSibling {
					args, seedCmd, seedSvc, err = seedFromSibling(out, root, main, insts, err)
				}
			} else {
				var ch PrepareChoice
				ch, err = resolvePrepare(root, main, instanceDir, insts)
				args = ch.Args
				if fromSibling = ch.Sibling != nil; fromSibling {
					args, seedCmd, seedSvc, err = applySibling(out, root, ch)
				}
			}
			if err == nil {
				err = copyAdapterFromMain(out, root, main, args)
			}
		}
		if err != nil {
			return err
		}
		// Sibling references go BEFORE the person's flags: the last --secret
		// of a name wins, and an explicit flag must beat a sibling (D7).
		pbase := slices.Clone(head)
		names := make([]string, 0, len(sibling))
		for n := range sibling {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			pbase = append(pbase, "--secret", n+"="+sibling[n])
		}
		pbase = append(pbase, o.Global...)
		if len(names) > 0 {
			from := " from another worktree"
			if fromSibling {
				from = ""
			}
			fmt.Fprintf(out, "reusing secret references %s%s (values stay where they are)\n", strings.Join(names, ", "), from)
		}
		fmt.Fprintf(out, "preparing %s (%s)\n", project, branch)
		var pOut, pErr bytes.Buffer
		code, err := runPrepare(append(append(append([]string{}, pbase...), "prepare"), args...), &pOut, &pErr)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("prepare exited with status %d", code)
		}
		if w := strings.TrimSpace(pErr.String()); w != "" {
			fmt.Fprintln(out, w) // out is the step writer: it redacts with the preflight's values
		}
		if seedCmd != "" {
			recordSeedRecipe(instanceDir, seedCmd, seedSvc)
		}
		return nil
	}
	up := func(out io.Writer, services []string) error {
		fmt.Fprintf(out, "starting %s\n", project)
		args := append([]string{}, base...)
		args = append(args, "up", "--in-run", "--mode", mode)
		if o.Reset {
			args = append(args, "--build") // the images are rebuilt from the code in the worktree
		}
		if o.Live != nil { // never down, --volumes or --force-recreate: only --build, for the services that changed
			var build bool
			if build, services = o.Live.UpArgs(services); build {
				args = append(args, "--build")
			}
		}
		for _, s := range services {
			args = append(args, "--service", s)
		}
		defer markInRun(instanceDir)()
		code, err := run(args, out, out)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("up exited with status %d", code)
		}
		return nil
	}

	runner.Out, runner.Err, runner.Root, runner.Branch, runner.Project = out, errOut, root, branch, project
	runner.InstanceDir, runner.LegacyBase, runner.Prepare, runner.Up = instanceDir, o.LegacyStateBase, prepare, up
	runner.Host = func(w io.Writer, step knowledge.Step) error {
		return lifecycle.HostStart(w, w, base, root, instanceDir, step)
	}
	runner.Run, runner.Prepared = rec, func() bool { return !o.Reset && !needsPrepare(instanceDir, root, branch, project) }
	runner.FreshData = o.Reset
	if err := runner.Execute(); err != nil {
		return err
	}
	verb := "started"
	if o.Reset {
		verb = "reset and started"
	}
	if o.Live != nil {
		verb = "reconciled"
	}
	fmt.Fprintf(out, "%s %s; see `stackfork --root %s urls`\n", verb, project, root)
	return nil
}

// reuseMainRecipe is the prepare step of reset --from-main (D5, D6): it
// checks the plan is still the confirmed one, moves the worktree's
// untracked adapter files aside, copies the main checkout's, and returns the
// main recipe's prepare args.
func reuseMainRecipe(out io.Writer, root string, o StartOptions) ([]string, error) {
	p, err := mainRecipe(root, o.NewStateBase, o.LegacyStateBase) // what is on disk now
	if err != nil {
		return nil, err
	}
	if o.Fingerprint != "" && o.Fingerprint != p.fp {
		return nil, errors.New("main's recipe changed during the reset (after the volumes were removed); run `stackfork reset --from-main` again to review the new recipe")
	}
	r := p.r
	args := recipeArgs(r)
	if err := knowledge.CheckPrepareArgs(args); err != nil {
		return nil, fmt.Errorf("reusing the recipe of %s: %w", r.Project, err)
	}
	moved, err := repos.MoveListed(root, p.aside) // the checked list, not a new read
	for _, x := range moved.Moved {
		fmt.Fprintf(out, "moved %s aside to %s/%s\n", x, moved.Dir, x)
	}
	if err != nil {
		return nil, fmt.Errorf("%w; moved so far: %s", err, strings.Join(moved.Moved, ", "))
	}
	if len(moved.Moved) > 0 {
		fmt.Fprintf(out, "nothing was deleted; delete %s when you no longer need it\n", moved.Dir)
	}
	rep, err := r.Apply(root)
	if err != nil {
		return nil, fmt.Errorf("reusing the recipe of %s: %w", r.Project, err)
	}
	fmt.Fprintf(out, "reusing the recipe of %s (%s): %s\n", r.Project, r.Source, strings.Join(args, " "))
	printApplied(out, rep, r.EnvFile, root, "")
	return args, nil
}

// globalFlagValues are the values of the global flag name (--name V,
// --name=V, -name V or -name=V) in the forwarded global flags.
func globalFlagValues(global []string, name string) []string {
	var vals []string
	for i := 0; i < len(global); i++ {
		n, v, hasEq := strings.Cut(strings.TrimLeft(global[i], "-"), "=")
		if !strings.HasPrefix(global[i], "-") || n != name {
			continue
		}
		if !hasEq {
			if i+1 >= len(global) {
				break
			}
			i++
			v = global[i]
		}
		vals = append(vals, v)
	}
	return vals
}

func hasSecretFlag(global []string) bool { return len(globalFlagValues(global, "secret")) > 0 }

// checkSecretFlags refuses a --secret that is not env: or npmrc: (design
// D12) before anything runs.
func checkSecretFlags(global []string) error {
	for _, raw := range globalFlagValues(global, "secret") {
		if _, _, err := compose.ParseCommandSecretFlag(raw); err != nil {
			return err
		}
	}
	return nil
}

// needsPrepare is true when the checkout has no usable prepared instance:
// no manifest, a snapshot that no longer matches, or an input file that
// changed since prepare.
func needsPrepare(instanceDir, root, branch, project string) bool {
	info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "up")
	if err != nil {
		return true
	}
	for path, digest := range info.Inputs {
		if h, err := state.SHA256File(path); err != nil || h != digest {
			return true
		}
	}
	// A mapped name the manifest lacks needs a new prepare (D7).
	mapped, _ := lifecycle.MappedSecrets(root)
	_, _, unprepared := lifecycle.InstanceSecrets(info, mapped)
	return len(unprepared) > 0
}

// Where the prepare arguments of a Start come from (PrepareChoice.From).
const (
	prepareFromManifest     = "manifest"      // the instance's manifest records its files
	prepareFromPlan         = "plan"          // the plan's prepare step
	prepareFromAdapterFiles = "adapter_files" // the knowledge file's adapter Compose files
	prepareFromAdapter      = "adapter"       // an adapter Compose file found in the checkout
	prepareFromSibling      = "sibling"       // the recipe of another prepared worktree
)

// PrepareChoice is what a Start prepares with, decided without writing
// anything: the args, where they come from, the sibling recipe when one is
// borrowed (the caller applies it) and Reuse when the instance's manifest
// supplies the files.
type PrepareChoice struct {
	Args    []string
	From    string
	Sibling *repos.SiblingRecipe
	Reuse   bool
}

// resolvePrepare is the prepare decision of a Start (not of reset): the
// instance's recorded files, the plan, the adapter files, else a prepared
// sibling's recipe. errNoRecipe when none helps. It reads files and runs
// `git ls-files` for a sibling; it copies and writes nothing.
func resolvePrepare(root, main, instanceDir string, insts []inventory.Instance) (PrepareChoice, error) {
	ch, err := prepareChoice(root, main, instanceDir)
	if errors.Is(err, errNoRecipe) {
		return siblingChoice(root, main, insts, err)
	}
	return ch, err
}

// prepareArgs decides what to pass to `prepare`: nothing when an instance
// manifest already records its files, else the recorded recipe's
// -f/--env-file, else the project's adapter compose files.
func prepareArgs(root, main, instanceDir string) ([]string, error) {
	ch, err := prepareChoice(root, main, instanceDir)
	return ch.Args, err
}

// prepareChoice is resolvePrepare without the sibling fallback.
func prepareChoice(root, main, instanceDir string) (PrepareChoice, error) {
	args, from, err := prepareArgsFrom(root, main, instanceDir)
	if err == nil && args != nil {
		if cerr := knowledge.CheckPrepareArgs(args); cerr != nil {
			return PrepareChoice{}, cerr
		}
	}
	return PrepareChoice{Args: args, From: from, Reuse: from == prepareFromManifest}, err
}

func prepareArgsFrom(root, main, instanceDir string) ([]string, string, error) {
	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err == nil {
		return nil, prepareFromManifest, nil // prepare reuses the files, env file and secrets it recorded
	}
	k, err := knowledge.LoadFirst(root, main)
	if err != nil {
		return nil, "", err
	}
	if k != nil {
		if args, from, err := knowledgePrepareArgsFrom(k); args != nil || err != nil {
			return args, from, err
		}
	}
	for _, dir := range []string{".stackfork", ".agent-dev"} {
		if m, _ := filepath.Glob(filepath.Join(root, dir, "*.compose.yaml")); len(m) > 0 {
			sort.Strings(m)
			rel, _ := filepath.Rel(root, m[0])
			return fileArgs([]string{rel}), prepareFromAdapter, nil
		}
	}
	if main != root {
		for _, dir := range []string{".stackfork", ".agent-dev"} {
			if m, _ := filepath.Glob(filepath.Join(main, dir, "*.compose.yaml")); len(m) > 0 {
				return nil, "", fmt.Errorf("the adapter %s exists only in the main checkout, not in this worktree; list it under adapter_files in .stackfork/knowledge.json so `worktree add` copies it, or copy it here",
					filepath.Join(dir, filepath.Base(m[0])))
			}
		}
	}
	return nil, "", errNoRecipe
}

// knowledgePrepareArgs are the prepare args knowledge gives: the plan's
// prepare step, else the adapter Compose files. Both are checked.
func knowledgePrepareArgs(k *knowledge.Knowledge) ([]string, error) {
	args, _, err := knowledgePrepareArgsFrom(k)
	return args, err
}

func knowledgePrepareArgsFrom(k *knowledge.Knowledge) ([]string, string, error) {
	if args, err := planPrepareArgs(k); args != nil || err != nil {
		return args, prepareFromPlan, err
	}
	var files []string
	for _, f := range k.AdapterFiles {
		if strings.HasSuffix(f, ".compose.yaml") || strings.HasSuffix(f, ".compose.yml") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, "", nil
	}
	args := fileArgs(files)
	if err := knowledge.CheckPrepareArgs(args); err != nil {
		return nil, "", fmt.Errorf("adapter_files: %w", err)
	}
	return args, prepareFromAdapterFiles, nil
}

// planPrepareArgs is the plan's prepare step args. Load already validated
// them; the check is repeated because these args reach prepare, where a
// --secret would be recorded and resolved without the preflight (C1).
func planPrepareArgs(k *knowledge.Knowledge) ([]string, error) {
	for _, st := range k.Plan.Steps {
		if st.Kind == knowledge.KindPrepare && len(st.Args) > 0 {
			if err := knowledge.CheckPrepareArgs(st.Args); err != nil {
				return nil, fmt.Errorf("step %q: %w", st.ID, err)
			}
			return st.Args, nil
		}
	}
	return nil, nil
}

var errNoRecipe = errors.New("no recorded prepare recipe or adapter compose file for this project; run `stackfork prepare -f FILE` once")

// manifestInstances reads the instances' manifests only: no Docker call and
// no git check, for the decisions that must not touch either (a recipe gap,
// the checks of reset --from-main).
func manifestInstances(newBase, legacyBase string) []inventory.Instance {
	var out []inventory.Instance
	for _, b := range []struct {
		base, prefix string
		legacy       bool
	}{{newBase, identity.Prefix, false}, {legacyBase, identity.LegacyPrefix, true}} {
		paths, _ := state.Manifests(b.base, b.prefix)
		for _, p := range paths {
			m, err := state.ReadManifest(p)
			if err != nil || m == nil {
				continue
			}
			in := inventory.Instance{Project: m.Project, Root: m.Root, Branch: m.Branch, Legacy: b.legacy, Files: m.Files,
				Secrets: m.ResolvedSecrets(), SeedOnce: m.SeedOnce, SeedService: m.SeedService}
			if m.EnvFile != nil {
				in.EnvFile = *m.EnvFile
			}
			if fi, err := os.Stat(p); err == nil {
				in.Used = fi.ModTime()
			}
			out = append(out, in)
		}
	}
	return out
}

func collectInstances(newBase, legacyBase string) []inventory.Instance {
	inv, err := inventory.Collect(inventory.Options{NewStateBase: newBase, LegacyStateBase: legacyBase, Version: Version})
	if err != nil {
		return nil
	}
	return inv.Instances
}

// siblingChoice finds the prepared sibling whose recipe a checkout nothing is
// known about can borrow, read-only: it checks the args and writes nothing.
// none is the error to return when no sibling can help.
func siblingChoice(root, main string, insts []inventory.Instance, none error) (PrepareChoice, error) {
	r, ferr := repos.FindSiblingRecipe(main, root, "", insts)
	if r == nil {
		if ferr != nil {
			return PrepareChoice{}, fmt.Errorf("%w; the prepared instance of another worktree cannot be reused: %v", none, ferr)
		}
		return PrepareChoice{}, none
	}
	args := fileArgs(r.Files)
	if r.EnvFile != "" {
		args = append(args, "--env-file", r.EnvFile)
	}
	if cerr := knowledge.CheckPrepareArgs(args); cerr != nil {
		return PrepareChoice{}, fmt.Errorf("reusing the recipe of %s: %w", r.Project, cerr)
	}
	return PrepareChoice{Args: args, From: prepareFromSibling, Sibling: r}, nil
}

// seedFromSibling builds the prepare recipe for a checkout nothing is known
// about from another worktree of the same repo (design.md D6a): it copies
// the sibling's untracked adapter files, writes the rewritten runtime env,
// and returns the prepare flags and the secret references to reuse. none is
// the error to return when no sibling can help.
func seedFromSibling(out io.Writer, root, main string, insts []inventory.Instance, none error) (args []string, seedCmd, seedSvc string, err error) {
	ch, err := siblingChoice(root, main, insts, none)
	if err != nil {
		return nil, "", "", err
	}
	return applySibling(out, root, ch)
}

// applySibling is the writing half of seedFromSibling: it applies the
// borrowed recipe to root and returns the args and the sibling's seed recipe.
func applySibling(out io.Writer, root string, ch PrepareChoice) (args []string, seedCmd, seedSvc string, err error) {
	r, args := ch.Sibling, ch.Args
	rep, aerr := r.Apply(root)
	if aerr != nil {
		return nil, "", "", fmt.Errorf("reusing the recipe of %s: %w", r.Project, aerr)
	}
	fmt.Fprintf(out, "reusing the recipe of %s (%s): %s", r.Project, r.Source, strings.Join(r.Files, ", "))
	if r.EnvFile != "" {
		fmt.Fprintf(out, "; env file %s", r.EnvFile)
	}
	fmt.Fprintln(out)
	printApplied(out, rep, r.EnvFile, root, "")
	seedCmd, seedSvc = r.SeedOnce, r.SeedService
	if seedCmd == "" {
		// the sibling's decision may live only in its knowledge file
		_, seedCmd, seedSvc = learn.SeedDecision([]string{r.Source}, nil)
	}
	if learn.SeedActive(seedCmd) {
		fmt.Fprintln(out, "carrying the seed_once recipe (runs once, when this instance's data is first created)")
	}
	return args, seedCmd, seedSvc, nil
}

// printApplied prints what Apply did, one line per file.
func printApplied(out io.Writer, rep repos.SiblingReport, envFile, root, from string) {
	for _, c := range rep.Copied {
		fmt.Fprintf(out, "copied %s%s\n", c, from)
	}
	if len(rep.Rewritten) > 0 {
		fmt.Fprintf(out, "rewrote %s in %s to point at %s\n", strings.Join(rep.Rewritten, ", "), envFile, root)
	}
	for _, sk := range rep.Skipped {
		fmt.Fprintf(out, "skipped %s\n", sk)
	}
}

// copyAdapterFromMain copies, from the main checkout, the untracked adapter
// files the prepare args name and a linked worktree lacks (untracked files
// are not in a new worktree). A missing file tracked in main is an error.
func copyAdapterFromMain(out io.Writer, root, main string, args []string) error {
	r, err := repos.AdapterRecipeFromMain(main, root, args)
	if err != nil || r == nil {
		return err
	}
	rep, err := r.Apply(root)
	if err != nil {
		return fmt.Errorf("copying the adapter files from the main checkout: %w", err)
	}
	printApplied(out, rep, r.EnvFile, root, " from the main checkout")
	return nil
}

func fileArgs(files []string) []string {
	var a []string
	for _, f := range files {
		a = append(a, "-f", f)
	}
	return a
}

// recipePrepareArgs pulls the safe prepare flags out of a recorded recipe
// command ("stackfork prepare -f x --env-file y"); anything unexpected, or a
// shell variable, makes it give up and return nil.
func recipePrepareArgs(cmd string) []string {
	toks := strings.Fields(cmd)
	for i, t := range toks {
		if t != "prepare" {
			continue
		}
		var out []string
		rest := toks[i+1:]
		for j := 0; j < len(rest); j++ {
			switch f := rest[j]; f {
			case "-f", "--file", "--env-file", "--hostname":
				if j+1 >= len(rest) || strings.ContainsAny(rest[j+1], "$`;&|<>") {
					return nil
				}
				j++
				out = append(out, f, strings.Trim(rest[j], `"'`))
			case "--reassign-ports", "--random-ports":
				out = append(out, f)
			default:
				return nil
			}
		}
		return out
	}
	return nil
}

// cmdStartEnv is `stackfork start-env [CHECKOUT]`.
func cmdStartEnv(stdout, stderr io.Writer, subArgs []string, rootArg, newStateBase, legacyStateBase string, global []string) (int, error) {
	if isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "start-env", "start-env [--force] [--mode MODE] [CHECKOUT]")
		return 0, nil
	}
	force := false
	mode := ""
	var rest []string
	for i := 0; i < len(subArgs); i++ {
		a := subArgs[i]
		switch {
		case a == "--force":
			force = true
		case a == "--mode" && i+1 < len(subArgs):
			i++
			mode = subArgs[i]
		case strings.HasPrefix(a, "--mode="):
			mode = strings.TrimPrefix(a, "--mode=")
		default:
			rest = append(rest, a)
		}
	}
	if mode != "" && mode != knowledge.ModeContainer && mode != knowledge.ModeHybrid {
		return 2, fmt.Errorf("unknown mode %q (%s or %s)", mode, knowledge.ModeContainer, knowledge.ModeHybrid)
	}
	if len(rest) > 1 {
		return 2, errors.New("usage: stackfork start-env [--force] [--mode MODE] [CHECKOUT]")
	}
	checkout := rootArg
	if len(rest) == 1 {
		checkout = rest[0]
	}
	profiles, err := resolveProfileDirs(nil)
	if err != nil {
		return 0, err
	}
	return 0, StartEnv(stdout, stderr, StartOptions{
		Checkout: checkout, NewStateBase: newStateBase, LegacyStateBase: legacyStateBase,
		Global: global, ProfileDirs: profiles, Force: force, Mode: mode,
	})
}

// recordSeedRecipe persists a sibling's seed_once in the new instance's
// manifest, unless the checkout's own knowledge already carries one.
func recordSeedRecipe(instanceDir, cmd, svc string) {
	path := filepath.Join(instanceDir, "manifest.json")
	if m, _ := state.ReadManifest(path); m != nil && m.SeedOnce == "" {
		m.SeedOnce, m.SeedService = cmd, svc
		_ = state.WritePrivate(path, m)
	}
}

// runPrepare runs the prepare command; a variable so tests can print from it.
var runPrepare func([]string, io.Writer, io.Writer) (int, error)

func init() { runPrepare = run }

// flagSecrets are the references a command's global flags carry: --secret
// NAME=REF pairs and --npmrc PATH (the npm_token sugar). A malformed flag is
// left to prepare to report.
func flagSecrets(global []string) map[string]string {
	refs := map[string]string{}
	for _, p := range globalFlagValues(global, "npmrc") {
		if p, err := identity.ResolvePath(p); err == nil {
			refs["npm_token"] = "npmrc:" + p + "#npm.pkg.github.com"
		}
	}
	for _, raw := range globalFlagValues(global, "secret") {
		if name, ref, err := compose.ParseCommandSecretFlag(raw); err == nil {
			refs[name] = ref
		}
	}
	return refs
}

// siblingRefs is the one place that decides which references a sibling
// worktree lends this Start, before the preflight, and prepare records exactly
// these (design D7). Nothing when an instance is prepared already. With no
// recipe to copy, the references of the sibling whose recipe will be reused
// (read-only: Apply runs later, in prepare). With a recipe from knowledge or
// an adapter and no --secret flag, those of the most recent sibling that has
// any. The machine map's names and the flags' names are dropped: they win,
// and so is any keychain: or op:// reference (a sibling's is stale: the map
// is where those live).
func siblingRefs(note io.Writer, o StartOptions, root, main, instanceDir string, insts []inventory.Instance) (map[string]string, error) {
	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err == nil {
		return nil, nil
	}
	mapped, err := lifecycle.MappedSecrets(root)
	if err != nil {
		return nil, err
	}
	var refs map[string]string
	switch args, perr := prepareArgs(root, main, instanceDir); {
	case errors.Is(perr, errNoRecipe):
		if r, _ := repos.FindSiblingRecipe(main, root, "", insts); r != nil {
			refs = r.Secrets
		}
	case perr == nil && args != nil && !hasSecretFlag(o.Global):
		refs = repos.SiblingSecrets(main, root, insts)
	}
	flags := flagSecrets(o.Global)
	out := map[string]string{}
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r := refs[n]
		if mapped[n] != "" || flags[n] != "" {
			continue
		}
		if secretstore.IsStoreRef(r) {
			// Only a person's map holds a keychain: or op:// reference, and prepare
			// takes sibling references as --secret flags, which refuse them. A name
			// the map holds now is dropped above and reaches prepare through it.
			fmt.Fprintf(note, "note: sibling's %s uses a store reference; map it with `stackfork secret map %s %s=REF`\n", n, filepath.Base(main), n)
			continue
		}
		out[n] = r
	}
	return out, nil
}

// startPreflight is the secrets step of a Start. The references are the ones
// prepare is about to record: a sibling worktree's (siblingRefs), the
// manifest's, the machine map's and the flags' (design D7). The flags count
// only when prepare will run to record them.
func startPreflight(rec *runs.Run, o StartOptions, root, main, instanceDir, trigger string, sibling map[string]string, willPrepare bool) (lifecycle.SecretValues, error) {
	mapped, err := lifecycle.MappedSecrets(root)
	if err != nil {
		return nil, err
	}
	var manifest map[string]string
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
		manifest = m.ResolvedSecrets()
	}
	flags := flagSecrets(o.Global)
	if !willPrepare {
		flags = nil // nothing records them: the manifest's references are the ones `up` runs with
	}
	folder, _, err := lifecycle.FolderSecrets(root, manifest)
	if err != nil {
		return nil, err
	}
	refs, _ := lifecycle.Effective(
		lifecycle.Layer{Source: lifecycle.SourceSibling, Refs: sibling},
		lifecycle.Layer{Source: lifecycle.SourceManifest, Refs: manifest},
		lifecycle.Layer{Source: lifecycle.SourceFolder, Refs: folder},
		lifecycle.Layer{Source: lifecycle.SourceMap, Refs: mapped},
		lifecycle.Layer{Source: lifecycle.SourceFlag, Refs: flags})
	return lifecycle.Preflight{Run: rec, InstanceDir: instanceDir, Main: main, Refs: refs, Declared: lifecycle.DeclaredSecrets(root, main),
		Dashboard: trigger == "dashboard" || trigger == live.TriggerLive}.Do()
}
