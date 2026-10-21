package live

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

var baseSteps = []knowledge.Step{
	{ID: "up", Kind: knowledge.KindComposeUp},
	{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "bun run db:migrate"},
}

// fx is one repository with one instance, a fake clock and a fake Exec.
type fx struct {
	t              *testing.T
	root, inst, nb string
	now            time.Time
	r              *Reconciler
	mu             sync.Mutex
	jobs           []Job
	result         func(n int, j Job) (RunResult, error) // n counts calls from 1
}

func saveKnowledge(t *testing.T, root string, steps []knowledge.Step) knowledge.Plan {
	t.Helper()
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: steps}}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return k.Plan
}

func trust(t *testing.T, nb, root string, p knowledge.Plan) {
	t.Helper()
	if err := knowledge.WriteTrust(knowledge.TrustPath(nb, root), knowledge.PlanHash(p.Steps), knowledge.TrustSteps(p.Steps)); err != nil {
		t.Fatal(err)
	}
}

func newFx(t *testing.T, steps []knowledge.Step) *fx {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", tmp(t))
	f := &fx{t: t, root: tmp(t), nb: tmp(t), now: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	f.inst = filepath.Join(f.nb, "acme-web-feature-login")
	os.MkdirAll(f.inst, 0o700)
	write(t, f.root, ".git/HEAD", "ref: refs/heads/feature-login\n")
	plan := saveKnowledge(t, f.root, steps)
	bl := &Baseline{Branch: "feature-login", Digests: map[string]string{}}
	bl.SetPlan(plan)
	if err := bl.Save(f.inst); err != nil {
		t.Fatal(err)
	}
	f.result = func(int, Job) (RunResult, error) { return RunResult{ID: 1, Outcome: runs.Succeeded}, nil }
	f.r = &Reconciler{Now: func() time.Time { return f.now }, Exec: func(_ context.Context, _ Target, j Job) (RunResult, error) {
		f.mu.Lock()
		f.jobs = append(f.jobs, j)
		n := len(f.jobs)
		f.mu.Unlock()
		return f.result(n, j)
	}}
	return f
}

func (f *fx) target(cs ...Change) Target {
	return Target{Main: f.root, Root: f.root, Project: "acme-web-feature-login", InstanceDir: f.inst, Branch: "feature-login",
		State: "running", NewBase: f.nb, Changes: cs}
}

func (f *fx) reconcile(cs ...Change) Outcome {
	f.t.Helper()
	o, err := f.r.Reconcile(context.Background(), f.target(cs...))
	if err != nil {
		f.t.Fatal(err)
	}
	return o
}

func (f *fx) execs() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.jobs) }

var lock = []Change{{Path: "bun.lock", Category: CatBuild, Service: "web", Digest: "d1", Kind: "changed"}}

// --- 4.3 trust gate

func TestTrustGatePausesWithoutARun(t *testing.T) {
	host := knowledge.Step{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "bun dev"}
	f := newFx(t, append(append([]knowledge.Step{}, baseSteps...), host))
	old := knowledge.Plan{Steps: []knowledge.Step{{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "npm run dev"}}}
	trust(t, f.nb, f.root, old) // an earlier version of the host step was approved

	o := f.reconcile(Change{Path: ".stackfork/knowledge.json", Category: CatPlan, Digest: "p2", Kind: "changed"})
	if o.Status != Paused || o.Pause == nil || o.Pause.Reason != "plan needs trust" || o.Pause.Kind != PauseTrust {
		t.Fatalf("%+v", o)
	}
	if d := strings.Join(o.Pause.Diff, "\n"); !strings.Contains(d, "app") {
		t.Fatalf("the diff must name step app:\n%s", d)
	}
	if f.execs() != 0 {
		t.Fatal("no run before trust")
	}
	if list, _ := runs.List(f.inst); len(list) != 0 {
		t.Fatalf("a refused reconcile wrote %d runs", len(list))
	}
	if p, _ := ReadPause(f.nb, f.root); p == nil || p.Kind != PauseTrust {
		t.Fatalf("pause file: %+v", p)
	}
	// Trusting the plan and resuming lets the next reconcile through.
	trust(t, f.nb, f.root, knowledge.Plan{Steps: []knowledge.Step{host}})
	if err := Resume(f.nb, f.root); err != nil {
		t.Fatal(err)
	}
	if o := f.reconcile(lock...); o.Status != Ran || f.execs() != 1 {
		t.Fatalf("%+v", o)
	}
}

func TestExecOnlyChangeRunsAndRecordsPlanChanged(t *testing.T) {
	f := newFx(t, baseSteps) // baseline holds the digests of baseSteps
	changed := []knowledge.Step{baseSteps[0], baseSteps[1]}
	changed[1].Cmd = "bun run db:migrate:v2"
	saveKnowledge(t, f.root, changed)
	o := f.reconcile(
		Change{Path: ".stackfork/knowledge.json", Category: CatPlan, Digest: "p2", Kind: "changed"},
		Change{Path: "prisma/migrations/2/migration.sql", Category: CatMigrations, Digest: "m2", Kind: "added"})
	if o.Status != Ran || f.execs() != 1 {
		t.Fatalf("%+v", o)
	}
	j := f.jobs[0]
	if strings.Join(j.Cause.PlanChanged, ",") != "migrate" || !j.Migrate {
		t.Fatalf("%+v", j.Cause)
	}
	bl, _ := LoadBaseline(f.inst)
	if bl.Digests["prisma/migrations/2/migration.sql"] != "m2" || bl.Plan["migrate"] != PlanDigests(knowledge.Plan{Steps: changed})["migrate"] {
		t.Fatalf("baseline after a succeeded run: %+v", bl)
	}
}

func TestPlanChangeAloneRunsNothingAndSettles(t *testing.T) {
	f := newFx(t, baseSteps)
	o := f.reconcile(Change{Path: ".stackfork/knowledge.json", Category: CatPlan, Digest: "p2", Kind: "changed"})
	if o.Status != Settled || f.execs() != 0 {
		t.Fatalf("%+v", o)
	}
	if bl, _ := LoadBaseline(f.inst); bl.Digests[".stackfork/knowledge.json"] != "p2" {
		t.Fatal("the baseline must move or the change stays pending forever")
	}
}

// --- 4.4 targets and mode

func TestStoppedInstanceKeepsTheChange(t *testing.T) {
	f := newFx(t, baseSteps)
	tg := f.target(lock...)
	tg.State = "stopped"
	o, err := f.r.Reconcile(context.Background(), tg)
	if err != nil || o.Status != Waiting || f.execs() != 0 {
		t.Fatalf("%+v %v", o, err)
	}
	if bl, _ := LoadBaseline(f.inst); len(bl.Digests) != 0 {
		t.Fatal("a change for a stopped instance stays pending")
	}
	if list, _ := runs.List(f.inst); len(list) != 0 {
		t.Fatal("no run for a stopped instance")
	}
	// Started later: the same change reconciles in one run.
	if o := f.reconcile(lock...); o.Status != Ran || f.execs() != 1 {
		t.Fatalf("%+v", o)
	}
}

func TestPartialInstanceRuns(t *testing.T) {
	f := newFx(t, baseSteps)
	tg := f.target(lock...)
	tg.State = "partial"
	if o, _ := f.r.Reconcile(context.Background(), tg); o.Status != Ran {
		t.Fatalf("%+v", o)
	}
}

func TestSwitchedBranchWarnsAndRunsNothing(t *testing.T) {
	f := newFx(t, baseSteps)
	write(t, f.root, ".git/HEAD", "ref: refs/heads/main\n")
	o := f.reconcile(lock...)
	if o.Status != Waiting || o.Warning != "branch switched" || f.execs() != 0 {
		t.Fatalf("%+v", o)
	}
	write(t, f.root, ".git/HEAD", "ref: refs/heads/feature-login\n")
	if o := f.reconcile(lock...); o.Status != Ran {
		t.Fatalf("back on the instance's branch: %+v", o)
	}
}

func TestModeComesFromTheLastSucceededRun(t *testing.T) {
	host := knowledge.Step{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "bun dev"}
	steps := append(append([]knowledge.Step{}, baseSteps...), host)
	for _, c := range []struct{ configured, last, want string }{
		{"container", "hybrid", "hybrid"}, // the person switched back to container; the running instance is hybrid
		{"hybrid", "container", "container"},
		{"hybrid", "", "hybrid"}, // no run: the configured mode
	} {
		t.Run(c.configured+" configured, last run "+c.last, func(t *testing.T) {
			f := newFx(t, steps)
			trust(t, f.nb, f.root, knowledge.Plan{Steps: steps})
			cfg, _ := config.Load()
			if err := cfg.SetRepoMode(f.root, c.configured); err != nil {
				t.Fatal(err)
			}
			cfg.Save()
			if c.last != "" {
				r, err := runs.Begin(f.inst, runs.Meta{Command: "start-env", Trigger: "cli", Mode: c.last})
				if err != nil {
					t.Fatal(err)
				}
				r.Finish(nil)
				// a failed run of the other mode does not count
				bad, _ := runs.Begin(f.inst, runs.Meta{Command: "start-env", Trigger: "cli", Mode: "other"})
				bad.Finish(errors.New("boom"))
			}
			if o := f.reconcile(lock...); o.Status != Ran {
				t.Fatalf("%+v", o)
			}
			if f.jobs[0].Mode != c.want {
				t.Fatalf("mode %q, want %q", f.jobs[0].Mode, c.want)
			}
		})
	}
}

// --- 4.5 guardrails

func TestHeldRunLockSkipsAndKeepsTheChange(t *testing.T) {
	f := newFx(t, baseSteps)
	held, err := runs.Begin(f.inst, runs.Meta{Command: "start-env", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if o := f.reconcile(lock...); o.Status != Skipped || f.execs() != 0 {
		t.Fatalf("%+v", o)
	}
	held.Finish(nil)
	if o := f.reconcile(lock...); o.Status != Ran || f.execs() != 1 {
		t.Fatalf("after the lock is free: %+v", o)
	}
}

func TestExecReportingBusySkips(t *testing.T) {
	f := newFx(t, baseSteps)
	f.result = func(int, Job) (RunResult, error) { return RunResult{}, fmt.Errorf("start: %w", runs.ErrBusy) }
	o := f.reconcile(lock...)
	if o.Status != Skipped {
		t.Fatalf("%+v", o)
	}
	if p, _ := ReadPause(f.nb, f.root); p != nil {
		t.Fatalf("a lock lost at the last moment is not a pause: %+v", p)
	}
	// and it did not use up the 30 s spacing
	f.result = func(int, Job) (RunResult, error) { return RunResult{ID: 1, Outcome: runs.Succeeded}, nil }
	if o := f.reconcile(lock...); o.Status != Ran {
		t.Fatalf("%+v", o)
	}
}

func TestDashboardActionLockSkips(t *testing.T) {
	f := newFx(t, baseSteps)
	busy := true
	f.r.ActionLocked = func(p string) bool { return busy && p == "acme-web-feature-login" }
	if o := f.reconcile(lock...); o.Status != Skipped || f.execs() != 0 {
		t.Fatalf("%+v", o)
	}
	busy = false
	if o := f.reconcile(lock...); o.Status != Ran {
		t.Fatalf("%+v", o)
	}
}

func TestOneLiveRunAtATime(t *testing.T) {
	f := newFx(t, baseSteps)
	enter, release := make(chan struct{}), make(chan struct{})
	f.result = func(n int, _ Job) (RunResult, error) {
		if n == 1 {
			close(enter)
			<-release
		}
		return RunResult{ID: n, Outcome: runs.Succeeded}, nil
	}
	done := make(chan Outcome)
	go func() { o, _ := f.r.Reconcile(context.Background(), f.target(lock...)); done <- o }()
	<-enter
	other := f.target(lock...)
	other.Project, other.InstanceDir = "acme-web-other", tmp(t)
	o, _ := f.r.Reconcile(context.Background(), other)
	if o.Status != Skipped || !strings.Contains(o.Reason, "another live run") {
		t.Fatalf("%+v", o)
	}
	close(release)
	if o := <-done; o.Status != Ran {
		t.Fatalf("%+v", o)
	}
}

func TestSpacingOf30SecondsPerInstance(t *testing.T) {
	f := newFx(t, baseSteps)
	if o := f.reconcile(lock...); o.Status != Ran {
		t.Fatal(o)
	}
	f.now = f.now.Add(29 * time.Second)
	if o := f.reconcile(lock...); o.Status != Skipped || f.execs() != 1 {
		t.Fatalf("%+v", o)
	}
	// another instance is not held back by it
	other := f.target(lock...)
	other.Project, other.InstanceDir = "acme-web-other", tmp(t)
	if o, _ := f.r.Reconcile(context.Background(), other); o.Status != Ran {
		t.Fatalf("%+v", o)
	}
	f.now = f.now.Add(2 * time.Second)
	if o := f.reconcile(lock...); o.Status != Ran || f.execs() != 3 {
		t.Fatalf("%+v", o)
	}
}

func TestTwelveRunsAnHourThenARateLimitPause(t *testing.T) {
	f := newFx(t, baseSteps)
	for i := 0; i < 12; i++ {
		if o := f.reconcile(lock...); o.Status != Ran {
			t.Fatalf("run %d: %+v", i+1, o)
		}
		f.now = f.now.Add(31 * time.Second)
	}
	o := f.reconcile(lock...)
	if o.Status != Paused || o.Pause == nil || o.Pause.Reason != "rate limit: 12 live runs in the last hour" || o.Pause.Kind != PauseRate {
		t.Fatalf("%+v", o)
	}
	if f.execs() != 12 {
		t.Fatalf("%d runs", f.execs())
	}
	// paused: nothing runs, even an hour later, until a person resumes
	f.now = f.now.Add(2 * time.Hour)
	if o := f.reconcile(lock...); o.Status != Paused || f.execs() != 12 {
		t.Fatalf("%+v", o)
	}
	Resume(f.nb, f.root)
	if o := f.reconcile(lock...); o.Status != Ran {
		t.Fatalf("after resume: %+v", o)
	}
}

func TestFailedRunPausesAfterExactlyOneRun(t *testing.T) {
	f := newFx(t, baseSteps)
	f.result = func(n int, _ Job) (RunResult, error) {
		return RunResult{ID: 23, Outcome: runs.Failed, Step: "rebuild"}, errors.New("step rebuild failed")
	}
	o := f.reconcile(lock...)
	if o.Status != Ran || o.Pause == nil || o.Pause.Reason != "run 23 failed at step rebuild" || o.Pause.Run != 23 || o.Pause.Step != "rebuild" || o.Pause.At.IsZero() {
		t.Fatalf("%+v", o)
	}
	for i := 0; i < 20; i++ { // 10 simulated minutes, a tick every 30 s
		f.now = f.now.Add(30 * time.Second)
		if o := f.reconcile(lock...); o.Status != Paused {
			t.Fatalf("tick %d: %+v", i, o)
		}
	}
	if f.execs() != 1 {
		t.Fatalf("%d runs while paused", f.execs())
	}
	if bl, _ := LoadBaseline(f.inst); len(bl.Digests) != 0 {
		t.Fatal("a failed run leaves the change pending")
	}
	// Fixed and resumed: the pending change reconciles once.
	f.result = func(n int, _ Job) (RunResult, error) { return RunResult{ID: 24, Outcome: runs.Succeeded}, nil }
	if err := Resume(f.nb, f.root); err != nil {
		t.Fatal(err)
	}
	if o := f.reconcile(lock...); o.Status != Ran || o.Pause != nil || f.execs() != 2 {
		t.Fatalf("%+v", o)
	}
	if bl, _ := LoadBaseline(f.inst); bl.Digests["bun.lock"] != "d1" {
		t.Fatalf("%+v", bl)
	}
}

func TestInterruptedRunPauses(t *testing.T) {
	f := newFx(t, baseSteps)
	f.result = func(int, Job) (RunResult, error) {
		return RunResult{ID: 5, Outcome: runs.Interrupted, Step: "secrets"}, errors.New("interrupted")
	}
	if o := f.reconcile(lock...); o.Pause == nil || o.Pause.Reason != "run 5 interrupted at step secrets" {
		t.Fatalf("%+v", o)
	}
}

func TestRefusedBeforeARunPauses(t *testing.T) {
	f := newFx(t, baseSteps)
	f.result = func(int, Job) (RunResult, error) { return RunResult{}, errors.New("web is not ready") }
	o := f.reconcile(lock...)
	if o.Pause == nil || o.Pause.Kind != PauseRefused || !strings.Contains(o.Pause.Reason, "web is not ready") {
		t.Fatalf("%+v", o)
	}
	if o := f.reconcile(lock...); o.Status != Paused || f.execs() != 1 {
		t.Fatalf("a refusal repeated every tick would loop: %+v", o)
	}
}

func TestPauseIsPerRepository(t *testing.T) {
	f := newFx(t, baseSteps)
	WritePause(f.nb, f.root, Pause{Kind: PauseFailed, Reason: "run 1 failed", At: f.now})
	other := f.target(lock...)
	other.Main = tmp(t)
	other.Root = f.root
	if o, _ := f.r.Reconcile(context.Background(), other); o.Status == Paused {
		t.Fatalf("another repository is not paused: %+v", o)
	}
}
