package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	"github.com/pedropinheiro75/stackfork/internal/state"
)

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
	base := append([]string{"--state-dir", o.NewStateBase, "--legacy-state-dir", o.LegacyStateBase, "--root", root}, o.Global...)

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
	secrets := func() []redact.Secret { return runSecrets(instanceDir, o.Global) }
	rec, err := runs.Begin(instanceDir, runs.Meta{Command: "start-env", Trigger: trigger, Mode: mode, Project: project, Branch: branch,
		Out: out, Secrets: secrets, Observer: o.Observer})
	if err != nil {
		return err
	}
	// Set now, not at the end: a killed process leaves an interrupted run
	// that the card can still retry. A failed seed replaces it.
	rec.SetRetry("stackfork --root " + root + " start-env")
	defer func() { rec.Finish(retErr) }() // the run ends with the command, whatever path returned

	prepare := func(out io.Writer, _ []string) error {
		if !needsPrepare(instanceDir, root, branch, project) {
			fmt.Fprintf(out, "%s is already prepared\n", project)
			return lifecycle.ErrAlreadyPrepared
		}
		args, err := prepareArgs(root, main, instanceDir)
		pbase := base
		var seedCmd, seedSvc string
		var sibRefs []string // NAME=ref pairs taken from a sibling worktree
		fromSibling := errors.Is(err, errNoRecipe)
		if fromSibling {
			var secrets []string
			args, secrets, seedCmd, seedSvc, err = seedFromSibling(out, root, main, insts, err)
			for _, sec := range secrets {
				sibRefs = append(sibRefs, sec)
				pbase = append(append([]string{}, pbase...), "--secret", sec)
			}
		}
		if err != nil {
			return err
		}
		if !fromSibling && args != nil && !hasSecretFlag(o.Global) {
			// A recipe from knowledge or an adapter names files, not secrets:
			// take the registry token references a sibling instance runs with.
			sec := repos.SiblingSecrets(main, root, insts)
			names := make([]string, 0, len(sec))
			for n := range sec {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				sibRefs = append(sibRefs, n+"="+sec[n])
				pbase = append(append([]string{}, pbase...), "--secret", n+"="+sec[n])
			}
			if len(names) > 0 {
				fmt.Fprintf(out, "reusing secret references %s from another worktree (values stay where they are)\n", strings.Join(names, ", "))
			}
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
			// The step writer only knows the secrets of the manifest and --secret
			// flags; the references prepare received from a sibling are not in it.
			rw := redact.NewWriter(out, refSecrets(sibRefs))
			fmt.Fprintln(rw, w)
			rw.Close()
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

func hasSecretFlag(global []string) bool {
	for _, g := range global {
		if g == "--secret" || strings.HasPrefix(g, "--secret=") {
			return true
		}
	}
	return false
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
	return false
}

// prepareArgs decides what to pass to `prepare`: nothing when an instance
// manifest already records its files, else the recorded recipe's
// -f/--env-file, else the project's adapter compose files.
func prepareArgs(root, main, instanceDir string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err == nil {
		return nil, nil // prepare reuses the files, env file and secrets it recorded
	}
	k, err := knowledge.LoadFirst(root, main)
	if err != nil {
		return nil, err
	}
	if k != nil {
		for _, st := range k.Plan.Steps {
			if st.Kind == knowledge.KindPrepare && len(st.Args) > 0 {
				return st.Args, nil
			}
		}
		var files []string
		for _, f := range k.AdapterFiles {
			if strings.HasSuffix(f, ".compose.yaml") || strings.HasSuffix(f, ".compose.yml") {
				files = append(files, f)
			}
		}
		if len(files) > 0 {
			return fileArgs(files), nil
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
func seedFromSibling(out io.Writer, root, main string, insts []inventory.Instance, none error) (args, secrets []string, seedCmd, seedSvc string, err error) {
	r, ferr := repos.FindSiblingRecipe(main, root, "", insts)
	if r == nil {
		if ferr != nil {
			return nil, nil, "", "", fmt.Errorf("%w; the prepared instance of another worktree cannot be reused: %v", none, ferr)
		}
		return nil, nil, "", "", none
	}
	rep, aerr := r.Apply(root)
	if aerr != nil {
		return nil, nil, "", "", fmt.Errorf("reusing the recipe of %s: %w", r.Project, aerr)
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
	names := make([]string, 0, len(r.Secrets))
	for n := range r.Secrets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		secrets = append(secrets, n+"="+r.Secrets[n])
	}
	if len(names) > 0 {
		fmt.Fprintf(out, "reusing secret references %s (values stay where they are)\n", strings.Join(names, ", "))
	}
	seedCmd, seedSvc = r.SeedOnce, r.SeedService
	if seedCmd == "" {
		// the sibling's decision may live only in its knowledge file
		_, seedCmd, seedSvc = learn.SeedDecision([]string{r.Source}, nil)
	}
	if learn.SeedActive(seedCmd) {
		fmt.Fprintln(out, "carrying the seed_once recipe (runs once, when this instance's data is first created)")
	}
	return args, secrets, seedCmd, seedSvc, nil
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

// runSecrets are the values a run must redact: the instance's secret
// references, and the --secret references of the command (which prepare is
// about to record in the manifest).
func runSecrets(instanceDir string, global []string) []redact.Secret {
	var secrets []redact.Secret
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
		secrets = lifecycle.SecretsForRedaction(m)
	}
	var refs []string
	for i := 0; i+1 < len(global); i++ {
		if global[i] == "--secret" {
			refs = append(refs, global[i+1])
		}
	}
	return append(secrets, refSecrets(refs)...)
}

// refSecrets resolves NAME=ref pairs to the values to redact.
func refSecrets(refs []string) []redact.Secret {
	var secrets []redact.Secret
	for _, r := range refs {
		if name, ref, ok := strings.Cut(r, "="); ok {
			if v, err := compose.ResolveSecret(name, ref); err == nil {
				secrets = append(secrets, redact.Secret{Name: name, Value: v})
			}
		}
	}
	return secrets
}

// runPrepare runs the prepare command; a variable so tests can print from it.
var runPrepare func([]string, io.Writer, io.Writer) (int, error)

func init() { runPrepare = run }
