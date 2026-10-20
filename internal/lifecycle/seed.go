package lifecycle

import (
	"fmt"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SetSeeded records the seeded marker in the instance manifest (nil clears it).
func SetSeeded(instanceDir string, v *bool) { setSeed(instanceDir, v, false) }

func setSeed(instanceDir string, v *bool, pending bool) {
	path := filepath.Join(instanceDir, "manifest.json")
	m, err := state.ReadManifest(path)
	if err != nil || m == nil {
		return
	}
	m.Seeded, m.SeedPending = v, pending
	_ = state.WritePrivate(path, m)
}

// ResolveExecService picks the service an exec step runs in: the step's own,
// else the plan's app service, else the instance's primary HTTP service, else
// the only service. "" when none applies (or compose.json is not there yet).
func ResolveExecService(instanceDir string, step knowledge.Step, plan knowledge.Plan) string {
	if step.Service != "" {
		return step.Service
	}
	if plan.AppService != "" {
		return plan.AppService
	}
	if instanceDir == "" {
		return ""
	}
	data, _ := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	model, _ := compose.DecodeJSON(data)
	if service, _, _, _ := compose.PrimaryHTTPService(model); service != "" {
		return service
	}
	if top, _ := model.(map[string]interface{}); top != nil {
		if svcs, _ := top["services"].(map[string]interface{}); len(svcs) == 1 {
			for name := range svcs { // a single-service stack has one place to run it
				return name
			}
		}
	}
	return ""
}

// RunSeed execs cmd in service (default: the primary app service). On
// success it records seeded:true; on failure it leaves the marker alone and
// says how to retry. The caller holds the instance lock.
func RunSeed(stdout, stderr io.Writer, root, branch, project, instanceDir, newBase, legacyBase, cmd, service string, plan knowledge.Plan) bool {
	retry := "stackfork --instance " + project + " seed"
	service = ResolveExecService(instanceDir, knowledge.Step{Service: service}, plan)
	f := false
	SetSeeded(instanceDir, &f) // it ran (or cannot): no longer pending, a failure is retried by hand
	if service == "" {
		fmt.Fprintf(stderr, "not seeded: no service to run %q in; record one with `stackfork learn record recipe --seed-service SVC`, then run `%s`\n", cmd, retry)
		return false
	}
	fmt.Fprintf(stdout, "seeding the new database in %s: %s\n", service, cmd)
	// The last run's output is kept so the dashboard can say why it failed.
	if f, err := os.OpenFile(filepath.Join(instanceDir, state.SeedLog), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		defer f.Close()
		fmt.Fprintf(f, "$ %s\n", cmd)
		stdout, stderr = io.MultiWriter(stdout, f), io.MultiWriter(stderr, f)
	}
	code, err := Execute(stdout, stderr, "exec", root, branch, project, instanceDir, newBase, legacyBase,
		Options{Service: service, Command: []string{"sh", "-c", cmd}})
	if err != nil || code != 0 {
		fmt.Fprintf(stderr, "not seeded: the seed command failed (%v, status %d); retry with `%s`, or, if the data is already there, `%s --mark-done`\n", err, code, retry, retry)
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
// The recorded or detected migrate runs first: a brand-new database has no
// tables, and an app image's entrypoint may not migrate on its own.
func SeedRecipe(root, main, instanceDir string) (cmd, service string) {
	return SeedRecipeIn(root, main, instanceDir, PlanMode(root, main))
}

// LoadPlan is the plan start-env runs for this checkout (empty when none).
func LoadPlan(root, main string) knowledge.Plan {
	if k, _ := knowledge.LoadFirst(root, main); k != nil {
		return k.Plan
	}
	return knowledge.Plan{}
}

// PlanMode is the startup mode `up` and `seed` use when none is given: the
// machine's configuration for the repo, as start-env resolves it.
func PlanMode(root, main string) string {
	mode := knowledge.ModeContainer
	if cfg, err := config.Load(); err == nil {
		mode, _ = cfg.ModeForPlan(main, LoadPlan(root, main))
	}
	return mode
}

// SeedRecipeIn is SeedRecipe for an explicit mode: a data step the mode does
// not run gives no seed.
func SeedRecipeIn(root, main, instanceDir, mode string) (cmd, service string) {
	cmd, service = seedRecipeIn(root, main, instanceDir, mode)
	if cmd != "" { // a service this mode leaves out cannot run it: do not burn the one-time seed
		plan := LoadPlan(root, main)
		if run, _ := plan.ForMode(mode); contains(excludedServices(run), ResolveExecService(instanceDir, knowledge.Step{Service: service}, plan)) {
			return "", ""
		}
	}
	return cmd, service
}

func seedRecipeIn(root, main, instanceDir, mode string) (cmd, service string) {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	decided, c, svc := learn.SeedDecisionIn([]string{root, main}, m, mode)
	if decided {
		if !learn.SeedActive(c) {
			return "", ""
		}
		return withMigrate(root, main, c), svc
	}
	_, detected := learn.DetectSeed(root)
	if detected == "" {
		return "", ""
	}
	return withMigrate(root, main, detected), ""
}

func withMigrate(root, main, seed string) string {
	migrate := ""
	for _, d := range []string{root, main} {
		if k, err := knowledge.Load(filepath.Join(d, ".stackfork", "knowledge.json")); err == nil {
			if m := k.Plan.MigrateStep(); m != nil {
				migrate = m.Cmd
				break
			}
		}
	}
	if migrate == "" {
		migrate, _ = learn.DetectSeed(root)
	}
	if migrate == "" || strings.Contains(seed, migrate) {
		return seed
	}
	return migrate + " && " + seed
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
// again, except a seed still pending because the up that created the
// volume failed: the volume exists by then, so it is not read as old data.
func PlanSeed(root, main, instanceDir string) bool {
	return PlanSeedIn(root, main, instanceDir, PlanMode(root, main))
}

// PlanSeedIn is PlanSeed for an explicit mode. When the mode does not run the
// recorded data step it decides nothing: the seed stays undecided.
func PlanSeedIn(root, main, instanceDir, mode string) bool {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if m != nil && m.SeedPending {
		cmd, _ := SeedRecipeIn(root, main, instanceDir, mode)
		return cmd != ""
	}
	if m == nil || m.Seeded != nil {
		return false
	}
	if cmd, _ := SeedRecipeIn(root, main, instanceDir, mode); cmd == "" {
		return false
	}
	if dataVolumesExist(instanceDir) {
		SetSeeded(instanceDir, boolPtr(true))
		return false
	}
	setSeed(instanceDir, boolPtr(false), true)
	return true
}
