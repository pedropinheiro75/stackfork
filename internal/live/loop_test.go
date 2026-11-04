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
		Admit:   func(*config.Config, string) string { return "" }, // the fixtures sit outside a configured root
	}
	t.Cleanup(lf.l.Close) // Windows cannot remove the held live/.lock
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

// A checkout the person switched to another branch is not fast-forwarded: the
// upstream of the instance's branch is not that branch's.
func TestLoopDoesNotSyncACheckoutOnAnotherBranch(t *testing.T) {
	f := newFx(t, baseSteps)
	lf := newLoop(t, f, f.nb)
	syncs := 0
	lf.l.Sync = func(string, time.Duration) repos.SyncResult { syncs++; return repos.SyncResult{} }
	write(t, f.root, ".git/HEAD", "ref: refs/heads/other\n")
	lf.tick(0)
	lf.tick(10 * time.Minute)
	if syncs != 0 {
		t.Fatalf("synced %d times on another branch", syncs)
	}
	if w := lf.l.Status().Repos[0].Targets[0].Warnings; len(w) == 0 || w[0] != "branch switched" {
		t.Fatalf("warnings: %v", w)
	}
	write(t, f.root, ".git/HEAD", "ref: refs/heads/feature-login\n")
	lf.tick(10 * time.Minute)
	if syncs == 0 {
		t.Fatal("back on the branch, the sync resumes")
	}
}

// config.json is hand-editable: a live key outside the configured folders, or
// one that is not a main checkout, is ignored with an error in the status.
func TestLoopIgnoresLiveKeysThatAreNotMainCheckoutsUnderAConfiguredRoot(t *testing.T) {
	f := newFx(t, baseSteps)
	lf := newLoop(t, f, f.nb)
	lf.l.Admit = nil // the real check
	execs := 0
	lf.l.Targets = func(string) ([]Target, error) { execs++; return []Target{f.target()}, nil }

	lf.cfg = &config.Config{Live: map[string]bool{f.root: true}} // no configured folder
	lf.tick(0)
	rs := lf.l.Status().Repos[0]
	if execs != 0 || rs.Error == "" || len(rs.Targets) != 0 {
		t.Fatalf("outside every root: execs=%d %+v", execs, rs)
	}

	lf.cfg = &config.Config{Roots: []string{filepath.Dir(f.root)}, Live: map[string]bool{f.root: true}}
	lf.tick(0)
	if rs = lf.l.Status().Repos[0]; execs == 0 || rs.Error != "" || len(rs.Targets) != 1 {
		t.Fatalf("a main checkout under a root is watched: execs=%d %+v", execs, rs)
	}

	wt := filepath.Join(filepath.Dir(f.root), "wt")
	write(t, wt, ".git", "gitdir: "+f.root+"/.git/worktrees/wt\n") // a linked worktree is not a main checkout
	execs = 0
	lf.cfg = &config.Config{Roots: []string{filepath.Dir(f.root)}, Live: map[string]bool{wt: true}}
	lf.tick(0)
	if rs = lf.l.Status().Repos[0]; execs != 0 || rs.Error == "" {
		t.Fatalf("a linked worktree key: execs=%d %+v", execs, rs)
	}
}

// repos.Walk lists a root that is itself a repository, so it is admitted.
func TestAdmitRepoAcceptsARootThatIsTheMainCheckout(t *testing.T) {
	f := newFx(t, baseSteps)
	cfg := &config.Config{Roots: []string{f.root}}
	if why := AdmitRepo(cfg, f.root); why != "" {
		t.Fatalf("refused: %s", why)
	}
	if why := AdmitRepo(&config.Config{}, f.root); why == "" {
		t.Fatal("no configured folder must refuse")
	}
}

// git worktree list stores the typed case; on a case-insensitive filesystem
// the same directory in another case is the same file.
func TestSameFileIgnoresCaseOnACaseInsensitiveFS(t *testing.T) {
	dir := filepath.Join(tmp(t), "Repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(dir), "rEPO")
	if _, err := os.Stat(other); err != nil {
		t.Skip("case-sensitive filesystem")
	}
	if !sameFile(dir, other) {
		t.Errorf("%s and %s are the same directory", dir, other)
	}
	if sameFile(dir, filepath.Join(dir, "missing")) {
		t.Error("a missing path is never the same")
	}
}
