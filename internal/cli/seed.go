package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func boolPtr(b bool) *bool { return &b }

// setSeeded records the seeded marker in the instance manifest (nil clears it).
func setSeeded(instanceDir string, v *bool) {
	path := filepath.Join(instanceDir, "manifest.json")
	m, err := state.ReadManifest(path)
	if err != nil || m == nil {
		return
	}
	m.Seeded = v
	_ = state.WritePrivate(path, m)
}

// seedRecipe finds the recorded seed_once command and its service: the
// checkout's (or main checkout's) knowledge recipe first, else the manifest.
func seedRecipe(root, instanceDir string) (cmd, service string) {
	main := repos.MainRepoOf(root)
	for _, dir := range []string{root, main} {
		if dir == "" {
			continue
		}
		if k, err := knowledge.Load(filepath.Join(dir, ".stackfork", "knowledge.json")); err == nil && k.Recipe.SeedOnce != "" {
			return k.Recipe.SeedOnce, k.Recipe.SeedService
		}
	}
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
		return m.SeedOnce, m.SeedService
	}
	return "", ""
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

// runSeed execs the recorded seed_once command in its service. On success
// it records seeded:true; on failure it leaves false and says how to retry.
func runSeed(stdout, stderr io.Writer, root, branch, project, instanceDir, newBase, legacyBase string) bool {
	cmd, service := seedRecipe(root, instanceDir)
	retry := "stackfork --instance " + project + " seed"
	if service == "" {
		data, _ := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
		model, _ := compose.DecodeJSON(data)
		service, _, _, _ = compose.PrimaryHTTPService(model)
	}
	if service == "" {
		fmt.Fprintf(stderr, "not seeded: no service to run %q in; set --seed-service with `stackfork learn record recipe`, then run `%s`\n", cmd, retry)
		return false
	}
	fmt.Fprintf(stdout, "seeding %s once in %s: %s\n", project, service, cmd)
	code, err := lifecycle.Execute(stdout, stderr, "exec", root, branch, project, instanceDir, newBase, legacyBase,
		lifecycle.Options{Service: service, Command: []string{"sh", "-c", cmd}})
	if err != nil || code != 0 {
		fmt.Fprintf(stderr, "not seeded: the seed command failed (%v, status %d); retry with `%s`\n", err, code, retry)
		return false
	}
	setSeeded(instanceDir, boolPtr(true))
	fmt.Fprintf(stdout, "seeded %s\n", project)
	return true
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
		return 0, errors.New("no seed_once recorded for this project; record one with `stackfork learn record recipe --seed-once CMD`")
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
