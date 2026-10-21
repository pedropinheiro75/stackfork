package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func setSeeded(instanceDir string, v *bool) { lifecycle.SetSeeded(instanceDir, v) }

func boolPtr(b bool) *bool { return &b }

func seedRecipe(root, instanceDir string) (cmd, service string) {
	return lifecycle.SeedRecipe(root, repos.MainRepoOf(root), instanceDir)
}

func runSeed(stdout, stderr io.Writer, root, main, branch, project, instanceDir, newBase, legacyBase, mode string, plan knowledge.Plan, step *runs.Step) error {
	if mode == "" {
		mode = lifecycle.PlanMode(root, main)
	}
	cmd, service := lifecycle.SeedRecipeIn(root, main, instanceDir, mode)
	return lifecycle.RunSeed(stdout, stderr, root, main, branch, project, instanceDir, newBase, legacyBase, cmd, service, plan, step)
}

// cmdSeed is `stackfork seed [--force]`: runs the recorded seed_once unless
// the instance is already seeded.
func cmdSeed(stdout, stderr io.Writer, subArgs []string, root, branch, project, instanceDir, newBase, legacyBase string) (int, error) {
	fs := newFlagSet(stdout, "seed")
	force := fs.Bool("force", false, "")
	markDone := fs.Bool("mark-done", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	cmd, _ := seedRecipe(root, instanceDir)
	if cmd == "" {
		return 0, errors.New("nothing to seed: this project is recorded as never seeded, or no seed script was found; record one with `stackfork learn record recipe --seed-once CMD`")
	}
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if m == nil {
		return 0, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	if *markDone {
		// The data is already there (seeded by hand, or an earlier run that
		// half-failed): record it so nothing warns or reruns it.
		setSeeded(instanceDir, boolPtr(true))
		fmt.Fprintf(stdout, "%s recorded as seeded; nothing ran\n", project)
		return 0, nil
	}
	if m.Seeded != nil && *m.Seeded && !*force {
		fmt.Fprintf(stdout, "%s is already seeded; --force runs it again\n", project)
		return 0, nil
	}
	if *force && m.Seeded != nil && *m.Seeded {
		fmt.Fprintln(stderr, "warning: re-running the seed on an already-seeded database can duplicate or overwrite data")
	}
	if err := os.MkdirAll(instanceDir, 0o700); err != nil {
		return 0, err
	}
	main := repos.MainRepoOf(root)
	mode := lifecycle.PlanMode(root, main)
	plan := lifecycle.LoadPlan(root, main) // one load: the plan that is checked is the plan that runs
	if err := lifecycle.CheckSeedTrust(root, main, instanceDir, newBase, plan, mode); err != nil {
		return 0, err // an untrusted host seed: before the preflight and any Docker call
	}
	var vals lifecycle.SecretValues
	rec, err := runs.Begin(instanceDir, runs.Meta{Command: "seed", Trigger: envTrigger(), Mode: mode, Project: project, Branch: branch,
		Out: stdout, Secrets: func() []redact.Secret { return vals.Redactions() }})
	if err != nil {
		return 0, err
	}
	rec.SetRetry("stackfork --instance " + project + " seed")
	if vals, err = lifecycle.InstancePreflight(rec, stdout, root, instanceDir, envTrigger() == "dashboard"); err != nil {
		rec.Finish(err)
		return 0, err
	}
	entry, _ := lifecycle.SeedEntry(root, main, instanceDir, plan, mode)
	if entry.ID == "" { // cmd != "" above, so SeedEntry has an entry
		entry.ID = "seed"
	}
	step := rec.Step(entry.ID, knowledge.KindExec, lifecycle.StepText(entry))
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		rec.Finish(err)
		return 0, err
	}
	defer lock.Release()
	serr := runSeed(stdout, stderr, root, main, branch, project, instanceDir, newBase, legacyBase, mode, plan, step)
	if serr != nil {
		rec.SetRetry("stackfork --instance " + project + " seed")
		rec.Finish(fmt.Errorf("step %s failed: %v; retry with stackfork seed", entry.ID, serr))
		return 1, nil
	}
	rec.Finish(nil)
	return 0, nil
}
