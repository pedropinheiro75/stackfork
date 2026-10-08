package lifecycle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SetSeeded records the seeded marker in the instance manifest (nil clears it).
func SetSeeded(instanceDir string, v *bool) {
	path := filepath.Join(instanceDir, "manifest.json")
	m, err := state.ReadManifest(path)
	if err != nil || m == nil {
		return
	}
	m.Seeded = v
	_ = state.WritePrivate(path, m)
}

// SetSeedDecision records the seed_once decision ("none" = never) in the
// instance manifest.
func SetSeedDecision(instanceDir, cmd string) {
	path := filepath.Join(instanceDir, "manifest.json")
	if m, _ := state.ReadManifest(path); m != nil {
		m.SeedOnce = cmd
		_ = state.WritePrivate(path, m)
	}
}

// RunSeed execs cmd in service (default: the primary app service). On
// success it records seeded:true; on failure it leaves the marker alone and
// says how to retry. The caller holds the instance lock.
func RunSeed(stdout, stderr io.Writer, root, branch, project, instanceDir, newBase, legacyBase, cmd, service string) bool {
	retry := "stackfork --instance " + project + " seed"
	if service == "" {
		data, _ := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
		model, _ := compose.DecodeJSON(data)
		service, _, _, _ = compose.PrimaryHTTPService(model)
		if top, _ := model.(map[string]interface{}); service == "" && top != nil {
			if svcs, _ := top["services"].(map[string]interface{}); len(svcs) == 1 {
				for name := range svcs { // a single-service stack has one place to run it
					service = name
				}
			}
		}
	}
	if service == "" {
		fmt.Fprintf(stderr, "not seeded: no service to run %q in; record one with `stackfork learn record recipe --seed-service SVC`, then run `%s`\n", cmd, retry)
		return false
	}
	fmt.Fprintf(stdout, "seeding %s once in %s: %s\n", project, service, cmd)
	code, err := Execute(stdout, stderr, "exec", root, branch, project, instanceDir, newBase, legacyBase,
		Options{Service: service, Command: []string{"sh", "-c", cmd}})
	if err != nil || code != 0 {
		fmt.Fprintf(stderr, "not seeded: the seed command failed (%v, status %d); retry with `%s`\n", err, code, retry)
		return false
	}
	t := true
	SetSeeded(instanceDir, &t)
	fmt.Fprintf(stdout, "seeded %s\n", project)
	return true
}
