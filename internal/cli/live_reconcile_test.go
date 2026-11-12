package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	state_ "github.com/pedropinheiro75/stackfork/internal/state"
)

// reconcileFixture is a started instance with a Reconciler that runs jobs
// through StartEnv and the fake docker, on a clock the test moves.
func reconcileFixture(t *testing.T) (r *live.Reconciler, tg live.Target, clock *time.Time, log string) {
	t.Helper()
	repo, state, log, _ := liveFixture(t, runSteps)
	// the plan has a host step: a person trusted it on this machine
	if _, e, code := runCLI(t, "--state-dir", state, "--legacy-state-dir", t.TempDir(), "--root", repo, "plan", "--trust"); code != 0 {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	r = &live.Reconciler{Now: func() time.Time { return now }, Exec: LiveExec(state, t.TempDir(), nil, nil)}
	dir := onlyRunDir(t, state)
	m, _ := state_.ReadManifest(filepath.Join(dir, "manifest.json"))
	tg = live.Target{Main: repo, Root: repo, Project: filepath.Base(dir), InstanceDir: dir, Branch: m.Branch, State: "running", NewBase: state,
		Changes: []live.Change{{Path: "bun.lock", Category: live.CatBuild, Service: "web", Digest: "d1", Kind: "changed"}}}
	return r, tg, &now, log
}

func TestReconcileRunsThroughStartEnv(t *testing.T) {
	r, tg, _, log := reconcileFixture(t)
	off := fileSize(log)
	o, err := r.Reconcile(context.Background(), tg)
	if err != nil || o.Status != live.Ran || o.Run.Outcome != runs.Succeeded || o.Run.ID != 2 {
		t.Fatalf("%+v %v", o, err)
	}
	if got, _ := runs.Get(tg.InstanceDir, 2); got.Command != "live" || got.Trigger != "live" || got.Cause == nil {
		t.Fatalf("%+v", got)
	}
	assertNoForbiddenArgs(t, dockerAfter(t, log, off))
	bl, _ := live.LoadBaseline(tg.InstanceDir)
	if bl == nil || bl.Digests["bun.lock"] != "d1" {
		t.Fatalf("baseline: %+v", bl)
	}
}

// A failing build gives exactly one run and a pause, then nothing for ten
// simulated minutes; a held run lock gives no run at all.
func TestReconcileFailedBuildPausesAfterOneRun(t *testing.T) {
	r, tg, clock, log := reconcileFixture(t)
	t.Setenv("DOCKER_FAKE_EXIT", "1") // compose up fails
	o, err := r.Reconcile(context.Background(), tg)
	if err != nil || o.Status != live.Ran || o.Run.Outcome != runs.Failed || o.Pause == nil || o.Pause.Reason != "run 2 failed at step rebuild" {
		t.Fatalf("%+v %v", o, err)
	}
	off := fileSize(log)
	for i := 0; i < 20; i++ {
		*clock = clock.Add(30 * time.Second)
		if o, _ := r.Reconcile(context.Background(), tg); o.Status != live.Paused {
			t.Fatalf("tick %d: %+v", i, o)
		}
	}
	if list, _ := runs.List(tg.InstanceDir); len(list) != 2 || dockerAfter(t, log, off) != "" {
		t.Fatalf("%d runs; docker after the pause:\n%s", len(list), dockerAfter(t, log, off))
	}
	if bl, _ := live.LoadBaseline(tg.InstanceDir); bl != nil && bl.Digests["bun.lock"] != "" {
		t.Fatal("a failed run must leave the change pending")
	}
}

func TestReconcileHeldRunLockWritesNoRun(t *testing.T) {
	r, tg, _, _ := reconcileFixture(t)
	held, err := runs.Begin(tg.InstanceDir, runs.Meta{Command: "up", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if o, _ := r.Reconcile(context.Background(), tg); o.Status != live.Skipped {
		t.Fatalf("%+v", o)
	}
	held.Finish(nil)
	if list, _ := runs.List(tg.InstanceDir); len(list) != 2 { // start-env + the held one
		t.Fatalf("%d runs", len(list))
	}
	if o, _ := r.Reconcile(context.Background(), tg); o.Status != live.Ran {
		t.Fatalf("%+v", o)
	}
}

// The trust gate holds in container mode too: a host step edited upstream
// pauses live mode before any run or Docker call.
func TestReconcileUntrustedHostStepRunsNothing(t *testing.T) {
	r, tg, _, log := reconcileFixture(t)
	steps := append([]knowledge.Step{}, runSteps...)
	for i := range steps {
		if steps[i].ID == "app" {
			steps[i].Cmd = "npm run other" // never trusted here
		}
	}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: steps}}
	if err := verifiedFixture(k).Save(filepath.Join(tg.Root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	off := fileSize(log)
	tg.Changes = []live.Change{{Path: ".stackfork/knowledge.json", Category: live.CatPlan, Digest: "p2", Kind: "changed"},
		{Path: "bun.lock", Category: live.CatBuild, Service: "web", Digest: "d1", Kind: "changed"}}
	o, _ := r.Reconcile(context.Background(), tg)
	if o.Status != live.Paused || o.Reason != "plan needs trust" || dockerAfter(t, log, off) != "" {
		t.Fatalf("%+v", o)
	}
	if list, _ := runs.List(tg.InstanceDir); len(list) != 1 {
		t.Fatalf("%d runs", len(list))
	}
}

// Targets lists the instances of the repository's checkouts and no others.
func TestLiveTargetsAreTheRepositorysInstances(t *testing.T) {
	_, tg, _, _ := reconcileFixture(t)
	got, err := live.Targets(tg.NewBase, t.TempDir(), tg.Main, "")
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if g := got[0]; g.Project != tg.Project || g.Root != tg.Root || g.InstanceDir != tg.InstanceDir || g.Branch != tg.Branch || g.State == "" {
		t.Fatalf("%+v want %+v", g, tg)
	}
	if other, _ := live.Targets(tg.NewBase, t.TempDir(), t.TempDir(), ""); len(other) != 0 {
		t.Fatalf("another repository has no targets: %+v", other)
	}
}
