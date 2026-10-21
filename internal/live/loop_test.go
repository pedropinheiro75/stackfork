package live

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// loopFx is a Loop over the fx repository: Targets, Config and Sync are
// fakes, the clock is the fx clock.
type loopFx struct {
	*fx
	l   *Loop
	cfg *config.Config
}

func newLoop(t *testing.T, f *fx, nb string) *loopFx {
	t.Helper()
	lf := &loopFx{fx: f, cfg: &config.Config{Live: map[string]bool{f.root: true}}}
	lf.l = &Loop{
		NewBase: nb, Exec: f.r.Exec,
		Config:  func() (*config.Config, error) { return lf.cfg, nil },
		Targets: func(string) ([]Target, error) { return []Target{f.target()}, nil },
		Sync:    func(string, time.Duration) repos.SyncResult { return repos.SyncResult{} },
		Now:     func() time.Time { return f.now },
		Rand:    func() float64 { return 0.5 },
	}
	return lf
}

func (lf *loopFx) tick(d time.Duration) {
	lf.now = lf.now.Add(d)
	lf.l.Tick(context.Background())
}

func withCompose(t *testing.T, f *fx) {
	t.Helper()
	b := fmt.Sprintf(`{"services":{"web":{"build":{"context":%q,"dockerfile":"Dockerfile"}}}}`, f.root)
	if err := os.WriteFile(filepath.Join(f.inst, "compose.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoopCatchesUpDebouncesAndMovesTheBaseline(t *testing.T) {
	f := newFx(t, baseSteps)
	withCompose(t, f)
	write(t, f.root, "Dockerfile", "FROM scratch\n")
	lf := newLoop(t, f, f.nb)

	lf.tick(0) // first tick: the pending file starts the quiet period
	if f.execs() != 0 {
		t.Fatal("ran before the quiet period")
	}
	st := lf.l.Status()
	if len(st.Repos) != 1 || len(st.Repos[0].Targets[0].Pending) == 0 {
		t.Fatalf("status must show the pending change: %+v", st)
	}
	lf.tick(2 * time.Second)
	if f.execs() != 0 {
		t.Fatal("ran before 3 s")
	}
	lf.tick(1 * time.Second)
	if f.execs() != 1 || len(f.jobs[0].Rebuild) != 1 || f.jobs[0].Rebuild[0] != "web" {
		t.Fatalf("catch-up must give one rebuild of web: %+v", f.jobs)
	}
	lf.tick(time.Minute)
	if f.execs() != 1 {
		t.Fatal("a succeeded run moves the baseline: no second run")
	}
	if pend := lf.l.Status().Repos[0].Targets[0].Pending; len(pend) != 0 {
		t.Fatalf("pending after the run: %+v", pend)
	}
	if lf.l.Running() != 0 {
		t.Fatal("Running must be 0 between runs")
	}
}

func TestLoopSecondInstanceReportsElsewhereAndTakesOverOnRelease(t *testing.T) {
	f := newFx(t, baseSteps)
	withCompose(t, f)
	a := newLoop(t, f, f.nb)
	b := newLoop(t, f, f.nb)
	a.tick(0)
	b.tick(0)
	if !b.l.Status().Elsewhere {
		t.Fatal("the second loop must report another dashboard")
	}
	write(t, f.root, "Dockerfile", "FROM scratch\n")
	b.tick(5 * time.Second)
	b.tick(5 * time.Second)
	if f.execs() != 0 {
		t.Fatal("the second loop must not reconcile")
	}
	a.l.Close()
	b.tick(time.Second)
	if b.l.Status().Elsewhere {
		t.Fatal("the lock is free: the second loop takes over")
	}
}

func TestLoopIdleWithoutLiveReposHoldsNoLock(t *testing.T) {
	f := newFx(t, baseSteps)
	lf := newLoop(t, f, f.nb)
	lf.cfg = &config.Config{}
	lf.tick(0)
	if lf.l.lock != nil {
		t.Fatal("no repository has live mode on: no lock")
	}
	lf.cfg = &config.Config{Live: map[string]bool{f.root: true}}
	lf.tick(0)
	if lf.l.lock == nil {
		t.Fatal("a repository turned on is picked up on the next tick")
	}
	lf.cfg = &config.Config{}
	lf.tick(0)
	if lf.l.lock != nil || len(lf.l.Status().Repos) != 0 {
		t.Fatal("turning the last repository off releases the lock")
	}
}

func TestLoopPausedRepositoryPollsButDoesNotRun(t *testing.T) {
	f := newFx(t, baseSteps)
	withCompose(t, f)
	write(t, f.root, "Dockerfile", "FROM scratch\n")
	WritePause(f.nb, f.root, Pause{Kind: PauseFailed, Reason: "run 3 failed at step rebuild", At: f.now})
	lf := newLoop(t, f, f.nb)
	for i := 0; i < 4; i++ {
		lf.tick(5 * time.Second)
	}
	s := lf.l.Status().Repos[0]
	if f.execs() != 0 || s.State != StatePaused || s.Reason != "run 3 failed at step rebuild" || len(s.Targets[0].Pending) == 0 {
		t.Fatalf("exec=%d %+v", f.execs(), s)
	}
	Resume(f.nb, f.root)
	lf.tick(5 * time.Second)
	lf.tick(5 * time.Second)
	if f.execs() != 1 {
		t.Fatalf("resume reconciles the pending change once, got %d", f.execs())
	}
}

func TestLoopRunStopsAtCancelAndReleasesTheLock(t *testing.T) {
	f := newFx(t, baseSteps)
	lf := newLoop(t, f, f.nb)
	lf.l.Every = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { lf.l.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
	if lf.l.lock != nil {
		t.Fatal("lock must be released")
	}
}
