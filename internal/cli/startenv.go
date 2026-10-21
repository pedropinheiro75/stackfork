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
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// syncUpstream is repos.SyncUpstream; tests replace it.
var syncUpstream = repos.SyncUpstream

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
	Observer        func(runs.Event) // receives the run's state changes, may be nil
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
	var plan knowledge.Plan
	kk, err := knowledge.LoadFirst(root, main)
	if err != nil { // a broken file is not an empty plan
		return err
	}
	if kk != nil {
		plan = kk.Plan
	}
	mode := o.Mode
	if mode == "" {
		mode = knowledge.ModeContainer
		if cfg, err := config.Load(); err == nil {
			var note string
			if mode, note = cfg.ModeForPlan(main, plan); note != "" {
				fmt.Fprintln(out, "note: "+note)
			}
		}
	}
	runner := lifecycle.PlanRunner{NewBase: o.NewStateBase, Main: main, Mode: mode, Plan: plan}
	if err := runner.Check(); err != nil { // before any Docker call
		return err
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
	sibling, err := siblingRefs(o, root, main, instanceDir, insts)
	if err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err != nil {
		level, reason := repos.ReadinessOf(filepath.Base(main), append(repos.Checkouts(main), root), repos.DefaultBranchOf(main), insts, o.ProfileDirs)
		if level != repos.Ready {
			if !o.Force || level != repos.NeedsLearn {
				return fmt.Errorf("%s is %s (%s); stackfork only starts projects whose startup was learned and verified. Ask your agent to learn it: `stackfork repos --prompt %s %s`",
					filepath.Base(main), level, reason, filepath.Base(main), branch)
			}
			fmt.Fprintf(errOut, "warning: %s is not verified (%s); starting anyway because of --force\n", filepath.Base(main), reason)
		}
	}

	trigger := o.Trigger
	if trigger == "" {
		trigger = "cli"
	}
	var vals lifecycle.SecretValues // the preflight's values redact every step after it (D11)
	secrets := func() []redact.Secret { return vals.Redactions() }
	rec, err := runs.Begin(instanceDir, runs.Meta{Command: "start-env", Trigger: trigger, Mode: mode, Project: project, Branch: branch,
		Out: out, Secrets: secrets, Observer: o.Observer})
	if err != nil {
		return err
	}
	// Set now, not at the end: a killed process leaves an interrupted run
	// that the card can still retry. A failed seed replaces it.
	rec.SetRetry("stackfork --root " + root + " start-env")
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
	// from the next start.
	sync := rec.Step("sync", "sync", "fetch the upstream; fast-forward when clean")
	if msg := syncUpstream(root, 15*time.Second); msg != "" {
		fmt.Fprintln(sync.Start(), msg)
	} else {
		sync.Start()
	}
	sync.End(nil, 0)

	// secrets: resolve every effective reference before prepare, which already
	// calls Docker, and fail naming every problem (design D8).
	if vals, err = startPreflight(rec, o, root, main, instanceDir, trigger, sibling, needsPrepare(instanceDir, root, branch, project)); err != nil {
		return err
	}
	runValues.Store(inRunKey(instanceDir), vals)
	defer runValues.Delete(inRunKey(instanceDir))

	prepare := func(out io.Writer, _ []string) error {
		if !needsPrepare(instanceDir, root, branch, project) {
			fmt.Fprintf(out, "%s is already prepared\n", project)
			return lifecycle.ErrAlreadyPrepared
		}
		args, err := prepareArgs(root, main, instanceDir)
		var seedCmd, seedSvc string
		fromSibling := errors.Is(err, errNoRecipe)
		if fromSibling {
			args, seedCmd, seedSvc, err = seedFromSibling(out, root, main, insts, err)
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
	runner.Run, runner.Prepared = rec, func() bool { return !needsPrepare(instanceDir, root, branch, project) }
	if err := runner.Execute(); err != nil {
		return err
	}
	fmt.Fprintf(out, "started %s; see `stackfork --root %s urls`\n", project, root)
	return nil
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

// prepareArgs decides what to pass to `prepare`: nothing when an instance
// manifest already records its files, else the recorded recipe's
// -f/--env-file, else the project's adapter compose files.
func prepareArgs(root, main, instanceDir string) ([]string, error) {
	args, err := prepareArgsFrom(root, main, instanceDir)
	if err == nil && args != nil {
		// whatever the source (plan, adapter_files, a glob), these reach
		// prepare, where splitArgs lifts --secret, --npmrc and the like
		if cerr := knowledge.CheckPrepareArgs(args); cerr != nil {
			return nil, cerr
		}
	}
	return args, err
}

func prepareArgsFrom(root, main, instanceDir string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err == nil {
		return nil, nil // prepare reuses the files, env file and secrets it recorded
	}
	k, err := knowledge.LoadFirst(root, main)
	if err != nil {
		return nil, err
	}
	if k != nil {
		if args, err := knowledgePrepareArgs(k); args != nil || err != nil {
			return args, err
		}
	}
	for _, dir := range []string{".stackfork", ".agent-dev"} {
		if m, _ := filepath.Glob(filepath.Join(root, dir, "*.compose.yaml")); len(m) > 0 {
			sort.Strings(m)
			rel, _ := filepath.Rel(root, m[0])
			return fileArgs([]string{rel}), nil
		}
	}
	if main != root {
		for _, dir := range []string{".stackfork", ".agent-dev"} {
			if m, _ := filepath.Glob(filepath.Join(main, dir, "*.compose.yaml")); len(m) > 0 {
				return nil, fmt.Errorf("the adapter %s exists only in the main checkout, not in this worktree; list it under adapter_files in .stackfork/knowledge.json so `worktree add` copies it, or copy it here",
					filepath.Join(dir, filepath.Base(m[0])))
			}
		}
	}
	return nil, errNoRecipe
}

// knowledgePrepareArgs are the prepare args knowledge gives: the plan's
// prepare step, else the adapter Compose files. Both are checked.
func knowledgePrepareArgs(k *knowledge.Knowledge) ([]string, error) {
	if args, err := planPrepareArgs(k); args != nil || err != nil {
		return args, err
	}
	var files []string
	for _, f := range k.AdapterFiles {
		if strings.HasSuffix(f, ".compose.yaml") || strings.HasSuffix(f, ".compose.yml") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	args := fileArgs(files)
	if err := knowledge.CheckPrepareArgs(args); err != nil {
		return nil, fmt.Errorf("adapter_files: %w", err)
	}
	return args, nil
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

func collectInstances(newBase, legacyBase string) []inventory.Instance {
	inv, err := inventory.Collect(inventory.Options{NewStateBase: newBase, LegacyStateBase: legacyBase, Version: Version})
	if err != nil {
		return nil
	}
	return inv.Instances
}

// seedFromSibling builds the prepare recipe for a checkout nothing is known
// about from another worktree of the same repo (design.md D6a): it copies
// the sibling's untracked adapter files, writes the rewritten runtime env,
// and returns the prepare flags and the secret references to reuse. none is
// the error to return when no sibling can help.
func seedFromSibling(out io.Writer, root, main string, insts []inventory.Instance, none error) (args []string, seedCmd, seedSvc string, err error) {
	r, ferr := repos.FindSiblingRecipe(main, root, "", insts)
	if r == nil {
		if ferr != nil {
			return nil, "", "", fmt.Errorf("%w; the prepared instance of another worktree cannot be reused: %v", none, ferr)
		}
		return nil, "", "", none
	}
	rep, aerr := r.Apply(root)
	if aerr != nil {
		return nil, "", "", fmt.Errorf("reusing the recipe of %s: %w", r.Project, aerr)
	}
	fmt.Fprintf(out, "reusing the recipe of %s (%s): %s", r.Project, r.Source, strings.Join(r.Files, ", "))
	if r.EnvFile != "" {
		fmt.Fprintf(out, "; env file %s", r.EnvFile)
	}
	fmt.Fprintln(out)
	for _, c := range rep.Copied {
		fmt.Fprintf(out, "copied %s\n", c)
	}
	if len(rep.Rewritten) > 0 {
		fmt.Fprintf(out, "rewrote %s in %s to point at %s\n", strings.Join(rep.Rewritten, ", "), r.EnvFile, root)
	}
	for _, sk := range rep.Skipped {
		fmt.Fprintf(out, "skipped %s\n", sk)
	}
	args = fileArgs(r.Files)
	if r.EnvFile != "" {
		args = append(args, "--env-file", r.EnvFile)
	}
	if cerr := knowledge.CheckPrepareArgs(args); cerr != nil {
		return nil, "", "", fmt.Errorf("reusing the recipe of %s: %w", r.Project, cerr)
	}
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
func siblingRefs(o StartOptions, root, main, instanceDir string, insts []inventory.Instance) (map[string]string, error) {
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
	for n, r := range refs {
		if mapped[n] != "" || flags[n] != "" {
			continue
		}
		if secretstore.IsStoreRef(r) {
			// Only a person's map holds a keychain: or op:// reference, and prepare
			// takes sibling references as --secret flags, which refuse them. A name
			// the map holds now is dropped above and reaches prepare through it.
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
	refs, _ := lifecycle.Effective(
		lifecycle.Layer{Source: lifecycle.SourceSibling, Refs: sibling},
		lifecycle.Layer{Source: lifecycle.SourceManifest, Refs: manifest},
		lifecycle.Layer{Source: lifecycle.SourceMap, Refs: mapped},
		lifecycle.Layer{Source: lifecycle.SourceFlag, Refs: flags})
	return lifecycle.Preflight{Run: rec, InstanceDir: instanceDir, Main: main, Refs: refs, Declared: lifecycle.DeclaredSecrets(root, main),
		Dashboard: trigger == "dashboard"}.Do()
}
