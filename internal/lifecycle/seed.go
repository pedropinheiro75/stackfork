package lifecycle

import (
	"errors"
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
	"github.com/pedropinheiro75/stackfork/internal/runs"
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
// the only service. "" for a host exec step, and when none applies (or
// compose.json is not there yet).
func ResolveExecService(instanceDir string, step knowledge.Step, plan knowledge.Plan) string {
	if step.IsHostExec() { // it runs on the host: no service, never a default one
		return ""
	}
	if step.Service != "" {
		return step.Service
	}
	if plan.AppService != "" {
		return plan.AppService
	}
	if instanceDir == "" {
		return ""
	}
	model := snapshotModel(instanceDir)
	top, _ := model.(map[string]interface{})
	svcs, _ := top["services"].(map[string]interface{})
	// a mail catcher, a console or a web server that got the HTTP route is no place to run the app's command
	if service, _, _, _ := compose.PrimaryHTTPService(model); service != "" && !compose.UtilityService(svcs[service]) && !compose.WebServerService(svcs[service]) {
		return service
	}
	if len(svcs) == 1 {
		for name, raw := range svcs { // a single-service stack has one place to run it (psql, mysql and mongosh commands run in the database)
			if !compose.WebServerService(raw) {
				return name
			}
		}
	}
	return ""
}

func snapshotModel(instanceDir string) interface{} {
	data, _ := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	model, _ := compose.DecodeJSON(data)
	return model
}

// hostHint is the end of the error for a step that has no service to run in:
// when no service of the instance's stack can run an app command (one built
// from the checkout, the plan's app service, or the primary HTTP service when
// it is no database or utility), the command that records the step on the
// host; else today's --service advice, which the caller supplies. "" when the
// stack cannot be read.
func hostHint(instanceDir string, plan knowledge.Plan, id, cmd, once string) string {
	top, _ := snapshotModel(instanceDir).(map[string]interface{})
	svcs, _ := top["services"].(map[string]interface{})
	if len(svcs) == 0 {
		return ""
	}
	primary, _, _, _ := compose.PrimaryHTTPService(top)
	for name, raw := range svcs {
		sv, _ := raw.(map[string]interface{})
		if _, built := sv["build"]; built || name == plan.AppService ||
			(name == primary && !compose.DatabaseService(raw) && !compose.UtilityService(raw) && !compose.WebServerService(raw)) {
			return ""
		}
	}
	return hostRecordHint("no service of this stack runs the app", id, cmd, once)
}

// hostRecordHint is the hint that ends with the command recording the step on the
// host, or, for a command SplitArgv refuses (a host step is one command, never
// a shell line), the note that it needs a shell-free form.
func hostRecordHint(why, id, cmd, once string) string {
	if _, err := knowledge.SplitArgv(cmd); err != nil {
		return why + "; to run it on the host, the command needs a shell-free form (no &&, |, ;, redirects or substitutions): one command per step"
	}
	h := fmt.Sprintf("%s; to run it on the host with this instance's environment:\n  stackfork learn record step --id %s --kind exec --on host --cmd %q", why, id, cmd)
	if once != "" {
		h += " --once " + once
	}
	return h
}

// notFoundHint is the end of the error for a container exec that exited 126 or
// 127 in a database or utility service: the app command is not there, so the
// step likely belongs on the host. "" for any other service or status.
func notFoundHint(instanceDir, service string, code int, id, cmd, once string) string {
	if code != 126 && code != 127 {
		return ""
	}
	top, _ := snapshotModel(instanceDir).(map[string]interface{})
	svcs, _ := top["services"].(map[string]interface{})
	raw, ok := svcs[service]
	if !ok || !(compose.DatabaseService(raw) || compose.UtilityService(raw)) {
		return ""
	}
	return hostRecordHint("the command was not found in "+service+", a database or utility image", id, cmd, once)
}

// RunSeed execs cmd in service (default: the primary app service). On
// success it records seeded:true; on failure it leaves the marker alone and
// says how to retry. The caller holds the instance lock. With a step, the
// seed's output goes to the step's writer (and so to its log), not to
// stdout/stderr; the final failure message still goes to stderr. It returns
// nil when there was nothing to run.
func RunSeed(stdout, stderr io.Writer, root, main, branch, project, instanceDir, newBase, legacyBase, cmd, service string, plan knowledge.Plan, step *runs.Step) error {
	if cmd == "" { // nothing to run in this mode: leave the marker alone
		if step != nil && step.State == runs.Pending {
			step.Skip("no seed for this mode")
		}
		return nil
	}
	retry := "stackfork --instance " + project + " seed"
	hostSteps := hostSeedSteps(plan)
	if hostSteps != nil && cmd != hostSeedCmd(hostSteps) {
		hostSteps = nil // the command is not the plan's data step (a manifest recipe, say)
	}
	if hostSteps != nil {
		if err := requireTrust(newBase, main, plan); err != nil { // the plan whose steps run here, not an earlier load
			return failSeed(stderr, step, err)
		}
	} else if disk := hostSeedSteps(LoadPlan(root, main)); disk != nil && cmd == hostSeedCmd(disk) {
		// cmd is a host seed but this plan has no such steps (it changed, or a live run cut them): never run it in a container
		return failSeed(stderr, step, errors.New("the plan changed since it was checked; review it and run `stackfork plan --trust`"))
	}
	if hostSteps == nil {
		service = ResolveExecService(instanceDir, knowledge.Step{Service: service}, plan)
	}
	f := false
	SetSeeded(instanceDir, &f) // it ran (or cannot): no longer pending, a failure is retried by hand
	w, ew := stdout, stderr
	if step != nil {
		w = step.Start()
		ew = w
	}
	if hostSteps != nil {
		return runHostSeed(w, stderr, step, hostSteps, root, project, instanceDir, newBase, legacyBase, cmd, retry)
	}
	if service == "" {
		err := errors.New("no service to run the seed in")
		if step != nil {
			step.End(err, 0)
		}
		if h := hostHint(instanceDir, plan, "seed", seedCmdOf(plan, cmd), knowledge.OnceData); h != "" {
			err = errors.New("no service to run the seed in; " + h)
			fmt.Fprintf(stderr, "not seeded: %v\nthen run `%s`\n", err, retry)
			return err
		}
		fmt.Fprintf(stderr, "not seeded: no service to run %q in; record one with `stackfork learn record recipe --seed-service SVC`, then run `%s`\n", cmd, retry)
		return err
	}
	if have := SnapshotServices(instanceDir); len(have) > 0 && !contains(have, service) {
		err := fmt.Errorf("seed cannot run: its service %s is not in this stack (this stack has: %s). Record the right one with: stackfork learn record step --id seed --kind exec --once data --service SERVICE --cmd %q",
			service, strings.Join(have, ", "), cmd)
		if h := hostHint(instanceDir, plan, "seed", seedCmdOf(plan, cmd), knowledge.OnceData); h != "" {
			err = fmt.Errorf("seed cannot run: its service %s is not in this stack (this stack has: %s); %s", service, strings.Join(have, ", "), h)
		}
		if step != nil {
			step.End(err, 0)
		}
		fmt.Fprintf(stderr, "not seeded: %v\n", err)
		return err
	}
	fmt.Fprintf(w, "seeding the new database in %s: %s\n", service, cmd)
	code, err := Execute(w, ew, "exec", root, branch, project, instanceDir, newBase, legacyBase,
		Options{Service: service, Command: []string{"sh", "-c", cmd}})
	if err == nil && code != 0 {
		err = fmt.Errorf("exited with status %d", code)
	}
	if err != nil {
		if h := notFoundHint(instanceDir, service, code, "seed", seedCmdOf(plan, cmd), knowledge.OnceData); h != "" {
			err = fmt.Errorf("%v; %s", err, h)
		}
		if step != nil {
			step.End(err, code)
		}
		fmt.Fprintf(stderr, "not seeded: the seed command failed (%v, status %d); retry with `%s`, or, if the data is already there, `%s --mark-done`\n", err, code, retry, retry)
		return err
	}
	t := true
	SetSeeded(instanceDir, &t)
	fmt.Fprintf(w, "seeded new database with %s\n", cmd)
	if step != nil {
		step.End(nil, 0)
	}
	return nil
}

func failSeed(stderr io.Writer, step *runs.Step, err error) error {
	if step != nil {
		step.End(err, 0)
	}
	fmt.Fprintf(stderr, "not seeded: %v\n", err)
	return err
}

// runHostSeed is RunSeed's tail for a seed on the host: the steps run in
// order, each as its own argument vector, and the first failure stops it
// (what && does in a container).
func runHostSeed(w, stderr io.Writer, step *runs.Step, steps []knowledge.Step, root, project, instanceDir, newBase, legacyBase, cmd, retry string) error {
	fmt.Fprintf(w, "seeding the new database on the host: %s\n", cmd)
	var code int
	var err error
	for _, s := range steps {
		if code, err = HostExec(w, project, root, instanceDir, newBase, legacyBase, s); err == nil && code != 0 {
			err = fmt.Errorf("exited with status %d", code)
		}
		if err != nil {
			break
		}
	}
	if err != nil {
		if step != nil {
			step.End(err, code)
		}
		fmt.Fprintf(stderr, "not seeded: the seed command failed (%v, status %d); retry with `%s`, or, if the data is already there, `%s --mark-done`\n", err, code, retry, retry)
		return err
	}
	t := true
	SetSeeded(instanceDir, &t)
	fmt.Fprintf(w, "seeded new database with %s\n", cmd)
	if step != nil {
		step.End(nil, 0)
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

// seedCmdOf is the plan's own data step command, else cmd: the command the
// host suggestion records is the seed alone, not the migrate prefix.
func seedCmdOf(plan knowledge.Plan, cmd string) string {
	if s := plan.SeedStep(); s != nil {
		return s.Cmd
	}
	return cmd
}

// hostSeedSteps are the argument vectors a seed on the host runs, in order:
// the plan's data step when it is on: host, preceded by the first schema step
// when that is also on the host with the same dir (the migrate prefix, as
// migrate && seed in a container). nil when the seed is not on the host: an
// unrecorded plan, no data step, or an auto-detected seed.
func hostSeedSteps(plan knowledge.Plan) []knowledge.Step {
	if plan.Status != knowledge.PlanRecorded {
		return nil
	}
	seed := plan.SeedStep()
	if seed == nil || !seed.IsHostExec() {
		return nil
	}
	if m := plan.MigrateStep(); m != nil && m.IsHostExec() && m.Dir == seed.Dir && !strings.Contains(seed.Cmd, m.Cmd) {
		return []knowledge.Step{*m, *seed}
	}
	return []knowledge.Step{*seed}
}

// hostSeedCmd is the text of a host seed entry: both commands when the
// migrate prefixes the seed. It is shown, never run through a shell.
func hostSeedCmd(steps []knowledge.Step) string {
	if len(steps) == 2 {
		return steps[0].Cmd + "; then " + steps[1].Cmd
	}
	return steps[0].Cmd
}

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
	if k, _, _ := knowledge.LoadFor(root, main); k != nil {
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
	cmd, service, host := seedRecipeIn(root, main, instanceDir, mode)
	if cmd != "" && !host { // a host seed has no service to leave out // a service this mode leaves out cannot run it: do not burn the one-time seed
		plan := LoadPlan(root, main)
		if run, _ := modeSteps(instanceDir, plan, mode); contains(excludedServices(run), ResolveExecService(instanceDir, knowledge.Step{Service: service}, plan)) {
			return "", ""
		}
	}
	return cmd, service
}

func seedRecipeIn(root, main, instanceDir, mode string) (cmd, service string, host bool) {
	var m *state.Manifest
	if instanceDir != "" { // the Repos plan list has no instance yet
		m, _ = state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	}
	decided, c, svc := learn.SeedDecisionIn([]string{root, main}, m, mode)
	if decided {
		if !learn.SeedActive(c) {
			return "", "", false
		}
		if hs := hostSeedSteps(LoadPlan(root, main)); hs != nil && hs[len(hs)-1].Cmd == c {
			return hostSeedCmd(hs), "", true
		}
		return withMigrate(root, main, instanceDir, c, svc), svc, false
	}
	_, detected := learn.DetectSeed(root)
	if detected == "" {
		return "", "", false
	}
	return withMigrate(root, main, instanceDir, detected, ""), "", false
}

// seedAutoDetected reports whether the seed of mode is the checkout's own
// script, with no recorded decision.
func seedAutoDetected(root, main, instanceDir, mode string) bool {
	var m *state.Manifest
	if instanceDir != "" {
		m, _ = state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	}
	if decided, _, _ := learn.SeedDecisionIn([]string{root, main}, m, mode); decided {
		return false
	}
	_, detected := learn.DetectSeed(root)
	return detected != ""
}

// DraftSkip is the reason a step stackfork only guessed or proposed does not
// run: the checkout's knowledge is a draft, so nothing it did not record runs
// as a migrate or seed. label is "seed" or "migrate", once the step's once
// value.
func DraftSkip(label, id, once string) string {
	return fmt.Sprintf("%s not run: setup is a draft; record it with `stackfork learn record step --id %s --kind exec --once %s --cmd …` (or finish `stackfork learn verify`)", label, id, once)
}

// isDraft: the knowledge exists and was neither verified nor verified once.
func isDraft(k *knowledge.Knowledge) bool {
	return k != nil && (k.Status == "" || k.Status == knowledge.StatusDraft)
}

func draftCheckout(root, main string) bool {
	k, _, _ := knowledge.LoadFor(root, main)
	return isDraft(k)
}

// DraftSeedSkip is DraftSkip for the seed of mode, or "" when the seed runs:
// only a draft checkout's auto-detected seed (no recorded decision) is held back.
func DraftSeedSkip(root, main, instanceDir, mode string) string {
	if draftCheckout(root, main) && seedAutoDetected(root, main, instanceDir, mode) {
		return DraftSkip("seed", "seed", knowledge.OnceData)
	}
	return ""
}

// withMigrate puts the plan's first schema step before the seed, so a new
// database has its tables. The migrate runs in the seed's service, so a
// schema step that names another service is not prefixed: it runs at its
// own place in the plan.
func withMigrate(root, main, instanceDir, seed, seedService string) string {
	migrate := ""
	for _, d := range []string{root, main} {
		if k, err := knowledge.Load(filepath.Join(d, ".stackfork", "knowledge.json")); err == nil {
			if m := k.Plan.MigrateStep(); m != nil {
				if m.IsHostExec() { // a container seed is not prefixed by a host command
					return seed
				}
				if ResolveExecService(instanceDir, *m, k.Plan) != ResolveExecService(instanceDir, knowledge.Step{Service: seedService}, k.Plan) {
					return seed
				}
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

// SeedCoversSchema reports whether the seed command of mode runs the plan's
// schema step s itself (the migrate prefix), so that step has nothing to do.
func SeedCoversSchema(root, main, instanceDir string, plan knowledge.Plan, mode string, s knowledge.Step) bool {
	m := plan.MigrateStep()
	if m == nil || m.ID != s.ID {
		return false
	}
	cmd, _ := SeedRecipeIn(root, main, instanceDir, mode)
	if hs := hostSeedSteps(plan); hs != nil {
		return len(hs) == 2 && hs[0].ID == s.ID && cmd == hostSeedCmd(hs)
	}
	return strings.HasPrefix(cmd, m.Cmd+" && ")
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
