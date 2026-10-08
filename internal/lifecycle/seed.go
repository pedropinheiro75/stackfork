package lifecycle

import (
	"fmt"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"io"
	"os"
	"os/exec"
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
	fmt.Fprintf(stdout, "seeding the new database in %s: %s\n", service, cmd)
	code, err := Execute(stdout, stderr, "exec", root, branch, project, instanceDir, newBase, legacyBase,
		Options{Service: service, Command: []string{"sh", "-c", cmd}})
	if err != nil || code != 0 {
		fmt.Fprintf(stderr, "not seeded: the seed command failed (%v, status %d); retry with `%s`\n", err, code, retry)
		return false
	}
	t := true
	SetSeeded(instanceDir, &t)
	fmt.Fprintf(stdout, "seeded new database with %s\n", cmd)
	return true
}

func boolPtr(b bool) *bool { return &b }

// SeedRecipe returns the seed_once command to run and its service. A
// recorded decision wins ("none" disables seeding); with no decision the
// default is automatic: the script the checkout's scan detects.
func SeedRecipe(root, main, instanceDir string) (cmd, service string) {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	decided, c, svc := learn.SeedDecision([]string{root, main}, m)
	if decided {
		if !learn.SeedActive(c) {
			return "", ""
		}
		return c, svc
	}
	_, detected := learn.DetectSeed(root)
	return detected, ""
}

// dataVolumesExist reports whether any named volume of the instance's
// snapshot already exists in Docker (true too when it cannot tell, or the
// stack declares no volume: those are never seeded).
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
	if len(vols) == 0 {
		return true // nothing persistent is created, so nothing to seed
	}
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

// PlanSeed decides, before an `up`, whether this up is the one that creates
// the data and so should seed it. It records seeded:false (this up will
// seed) or seeded:true (data already there). Once decided it never runs
// again: restarts and later ups of a decided instance are left alone.
func PlanSeed(root, main, instanceDir string) bool {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if m == nil || m.Seeded != nil {
		return false
	}
	if cmd, _ := SeedRecipe(root, main, instanceDir); cmd == "" {
		return false
	}
	if dataVolumesExist(instanceDir) {
		SetSeeded(instanceDir, boolPtr(true))
		return false
	}
	SetSeeded(instanceDir, boolPtr(false))
	return true
}
