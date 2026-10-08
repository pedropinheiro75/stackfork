package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func boolPtr(b bool) *bool { return &b }

func setSeeded(instanceDir string, v *bool) { lifecycle.SetSeeded(instanceDir, v) }

// seedRecipe returns the active seed_once command and its service ("" when
// none was decided, or the decision is "never").
func seedRecipe(root, instanceDir string) (cmd, service string) {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	decided, c, svc := learn.SeedDecision([]string{root, repos.MainRepoOf(root)}, m)
	if !decided || !learn.SeedActive(c) {
		return "", ""
	}
	return c, svc
}

// seedDecided reports whether anyone decided about seeding this project yet.
func seedDecided(root, instanceDir string) bool {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	d, _, _ := learn.SeedDecision([]string{root, repos.MainRepoOf(root)}, m)
	return d
}

// persistSeedDecision stores the answer where decisions live: the instance
// manifest and, if the checkout has one, its knowledge recipe.
func persistSeedDecision(root, instanceDir, cmd string) {
	lifecycle.SetSeedDecision(instanceDir, cmd)
	_ = learn.RecordSeedDecision(root, cmd)
}

// dataVolumesExist reports whether any named volume of the instance's
// snapshot already exists in Docker.
func dataVolumesExist(instanceDir string) bool {
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return true // cannot tell: never seed
	}
	model, err := compose.DecodeJSON(data)
	if err != nil {
		return true
	}
	top, _ := model.(map[string]interface{})
	vols, _ := top["volumes"].(map[string]interface{})
	for logical, v := range vols {
		name := logical
		if vm, ok := v.(map[string]interface{}); ok {
			if n, ok := vm["name"].(string); ok && n != "" {
				name = n
			}
		}
		if exec.Command("docker", "volume", "inspect", name).Run() == nil {
			return true
		}
	}
	return false
}

// planSeed decides, before an `up`, whether this up is the one that creates
// the data and so should seed it. It records seeded:false (this up will
// seed) or seeded:true (data already there). Once decided it never runs
// again: restarts and later ups of a decided instance are left alone.
func planSeed(root, instanceDir string) bool {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if m == nil || m.Seeded != nil {
		return false
	}
	if cmd, _ := seedRecipe(root, instanceDir); cmd == "" {
		return false
	}
	if dataVolumesExist(instanceDir) {
		setSeeded(instanceDir, boolPtr(true))
		return false
	}
	setSeeded(instanceDir, boolPtr(false))
	return true
}

func runSeed(stdout, stderr io.Writer, root, branch, project, instanceDir, newBase, legacyBase string) bool {
	cmd, service := seedRecipe(root, instanceDir)
	return lifecycle.RunSeed(stdout, stderr, root, branch, project, instanceDir, newBase, legacyBase, cmd, service)
}

// cmdSeed is `stackfork seed [--force]`: runs the recorded seed_once unless
// the instance is already seeded.
func cmdSeed(stdout, stderr io.Writer, subArgs []string, root, branch, project, instanceDir, newBase, legacyBase string) (int, error) {
	fs := newFlagSet(stdout, "seed")
	force := fs.Bool("force", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	cmd, _ := seedRecipe(root, instanceDir)
	if cmd == "" {
		if seedDecided(root, instanceDir) {
			return 0, errors.New("this project is recorded as never seeded; change it with `stackfork learn record recipe --seed-once CMD`")
		}
		_, proposal := learn.DetectSeed(root)
		if proposal == "" {
			return 0, errors.New("no seed script found for this project; record one with `stackfork learn record recipe --seed-once CMD`")
		}
		persistSeedDecision(root, instanceDir, proposal) // running it is the decision
	}
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if m == nil {
		return 0, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
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
	if !runSeed(stdout, stderr, root, branch, project, instanceDir, newBase, legacyBase) {
		return 1, nil
	}
	return 0, nil
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
