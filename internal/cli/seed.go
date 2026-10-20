package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func setSeeded(instanceDir string, v *bool) { lifecycle.SetSeeded(instanceDir, v) }

func boolPtr(b bool) *bool { return &b }

func seedRecipe(root, instanceDir string) (cmd, service string) {
	return lifecycle.SeedRecipe(root, repos.MainRepoOf(root), instanceDir)
}

func runSeed(stdout, stderr io.Writer, root, branch, project, instanceDir, newBase, legacyBase, mode string, step *runs.Step) error {
	main := repos.MainRepoOf(root)
	if mode == "" {
		mode = lifecycle.PlanMode(root, main)
	}
	cmd, service := lifecycle.SeedRecipeIn(root, main, instanceDir, mode)
	return lifecycle.RunSeed(stdout, stderr, root, branch, project, instanceDir, newBase, legacyBase, cmd, service, lifecycle.LoadPlan(root, main), step)
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
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	if err := runSeed(stdout, stderr, root, branch, project, instanceDir, newBase, legacyBase, "", nil); err != nil {
		return 1, nil
	}
	return 0, nil
}
