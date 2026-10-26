package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// ErrRecordedPlan: the checkout's knowledge has plan steps, so it has a plan
// to show and nothing to derive (add-derived-plan-view D1).
var ErrRecordedPlan = errors.New("the plan has recorded steps")

// DeriveOptions is what DerivePlan needs. Instances is the inventory the
// caller already has; nil reads the instances' manifests (no Docker call).
// Out receives the mode note; nil discards it.
type DeriveOptions struct {
	Checkout        string
	NewStateBase    string
	LegacyStateBase string
	ProfileDirs     []string
	Mode            string // empty: the machine's configured mode
	Instances       []inventory.Instance
	Out             io.Writer
}

// DerivePlan is the plan a Start of the checkout would run, for a checkout
// whose knowledge is absent or has no plan steps. It calls the functions
// StartEnv calls (startPlan, PlanRunner.Check and Steps, startRefusal,
// resolvePrepare) and has no side effect: no file is written or copied, and
// neither Docker nor a host command runs. A broken knowledge file is an
// error; a plan with steps is ErrRecordedPlan.
func DerivePlan(o DeriveOptions) (lifecycle.DerivedPlan, error) {
	toplevel, err := gitToplevel(o.Checkout)
	if err != nil {
		return lifecycle.DerivedPlan{}, fmt.Errorf("%s is not a git checkout", o.Checkout)
	}
	root, err := identity.ResolvePath(toplevel)
	if err != nil {
		return lifecycle.DerivedPlan{}, err
	}
	branch, err := currentBranch(root)
	if err != nil {
		return lifecycle.DerivedPlan{}, err
	}
	main := repos.MainRepoOf(root)
	if main == "" {
		main = root
	}
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	plan, mode, err := startPlan(out, StartOptions{Mode: o.Mode}, root, main)
	if err != nil {
		return lifecycle.DerivedPlan{}, err
	}
	if len(plan.Steps) > 0 {
		return lifecycle.DerivedPlan{}, ErrRecordedPlan
	}
	insts := o.Instances
	if insts == nil {
		insts = manifestInstances(o.NewStateBase, o.LegacyStateBase)
	}
	d := lifecycle.DerivedPlan{Mode: mode, Steps: []lifecycle.PlanStep{}}
	project, instanceDir, err := checkoutInstance(root, branch, o.NewStateBase, o.LegacyStateBase)
	if err != nil {
		return d, err
	}
	runner := lifecycle.PlanRunner{NewBase: o.NewStateBase, Main: main, Root: root, InstanceDir: instanceDir, Mode: mode, Plan: plan}
	// The order of StartEnv: the plan check, the readiness check, then the
	// prepare recipe, which `prepare` resolves.
	if err := runner.Check(); err != nil {
		d.Refusal = err.Error()
		return d, nil
	}
	if _, err := startRefusal(root, main, branch, instanceDir, insts, o.ProfileDirs, false); err != nil {
		d.Refusal = err.Error()
		return d, nil
	}
	ch, err := resolvePrepare(root, main, instanceDir, insts)
	if err != nil {
		d.Refusal = err.Error()
		return d, nil
	}
	d.PrepareFrom = ch.From
	manifest, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	prepared := ch.Reuse && !needsPrepare(instanceDir, root, branch, project)
	for _, s := range runner.Steps() {
		ps := lifecycle.NewPlanStep(s, lifecycle.StateDerived)
		switch {
		case s.Kind == knowledge.KindPrepare:
			ps.Source = prepareSource(ch, manifest, prepared, main)
			ps.Args = ch.Args
			if ch.Reuse && manifest != nil {
				ps.Args = manifestArgs(manifest)
			}
		case s.Kind == knowledge.KindComposeUp:
			ps.Source = "implicit: starts every service"
		case s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData:
			ps.Source = seedSource(s, manifest)
			if note := lifecycle.DraftSeedSkip(root, main, instanceDir, mode); note != "" {
				ps.Source = note
			}
		}
		ps.Step.Args = ps.Args // the text of the row names the files
		d.Steps = append(d.Steps, ps)
	}
	// What the app's own start already runs is shown, not run again.
	k, _, _ := knowledge.LoadFor(root, main)
	inMigrate, inSeed := learn.EntrypointRuns(k)
	for _, n := range []struct {
		id  string
		run *knowledge.StartupRun
	}{{"migrate", inMigrate}, {"seed", inSeed}} {
		if n.run != nil {
			ps := lifecycle.NewPlanStep(learn.EntrypointNote(n.id, n.run), lifecycle.StateDerived)
			ps.Source = "found by learn in the app's start"
			d.Steps = append(d.Steps, ps)
		}
	}
	if main != root {
		files := argFiles(ch.Args, root)
		if ch.Reuse && manifest != nil {
			files = manifest.Files
		}
		d.Gap = repos.RecipeGap(main, root, files, insts).Message(root)
	}
	return d, nil
}

// manifestArgs are the prepare flags a prepared instance's manifest records.
func manifestArgs(m *state.Manifest) []string {
	args := fileArgs(m.Files)
	if m.EnvFile != nil && *m.EnvFile != "" {
		args = append(args, "--env-file", *m.EnvFile)
	}
	return args
}

// argFiles are the Compose files of prepare args, as paths in root.
func argFiles(args []string, root string) []string {
	var files []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" || args[i] == "--file" {
			f := args[i+1]
			if !filepath.IsAbs(f) {
				f = filepath.Join(root, f)
			}
			files = append(files, f)
		}
	}
	return files
}

// prepareSource says where the prepare step's files come from (D3).
func prepareSource(ch PrepareChoice, m *state.Manifest, prepared bool, main string) string {
	switch ch.From {
	case prepareFromManifest:
		files := ""
		if m != nil {
			files = strings.Join(manifestArgs(m), " ")
		}
		if prepared {
			return "already prepared: reuses " + files
		}
		return "prepares again with the files the instance recorded: " + files
	case prepareFromPlan:
		return "prepare step in knowledge.json"
	case prepareFromAdapterFiles:
		return "adapter_files in knowledge.json"
	case prepareFromAdapter:
		return "adapter " + strings.Join(argFilesRel(ch.Args), ", ")
	case prepareFromSibling:
		where := ch.Sibling.Source
		if rel, err := filepath.Rel(main, where); err == nil && !strings.HasPrefix(rel, "..") {
			where = filepath.ToSlash(rel)
		}
		src := fmt.Sprintf("recipe of %s (%s)", ch.Sibling.Project, where)
		if decided, _, _ := learn.SeedDecision([]string{ch.Sibling.Source}, nil); decided || ch.Sibling.SeedOnce != "" {
			src += "; the sibling's seed decision applies after prepare"
		}
		return src
	}
	return ch.From
}

func argFilesRel(args []string) []string {
	var l []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" || args[i] == "--file" {
			l = append(l, args[i+1])
		}
	}
	return l
}

// seedSource says where the seed entry comes from (D3): a recorded seed
// recipe in the instance's manifest, else the script stackfork detected.
func seedSource(s knowledge.Step, m *state.Manifest) string {
	if m != nil && m.SeedOnce != "" {
		return "recorded in the instance's manifest; runs once, on a new database"
	}
	return "auto-detected: " + s.Cmd + "; runs once, on a new database"
}
