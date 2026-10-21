package live

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// cleanupLoopFx is a Loop over the cleanup fixture, on a clock the test moves.
type cleanupLoopFx struct {
	*cleanupFx
	l        *Loop
	cfg      *config.Config
	now      time.Time
	fetches  int
	fetchErr error
	downs    int
}

func newCleanupLoop(t *testing.T, cl config.Cleanup) *cleanupLoopFx {
	t.Helper()
	f := &cleanupLoopFx{cleanupFx: newCleanupFx(t), now: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	f.cfg = &config.Config{Live: map[string]bool{f.Main: true}, Cleanup: map[string]config.Cleanup{f.Main: cl}}
	f.l = &Loop{
		NewBase: f.State,
		Config:  func() (*config.Config, error) { return f.cfg, nil },
		Targets: func(string) ([]Target, error) { return []Target{f.Inst}, nil },
		Sync:    func(string, time.Duration) repos.SyncResult { return repos.SyncResult{} },
		Exec: func(context.Context, Target, Job) (RunResult, error) {
			return RunResult{}, errors.New("no live run expected")
		},
		Now:  func() time.Time { return f.now },
		Rand: func() float64 { return 0.5 },
		GH:   &cleanup.GH{Paths: []string{}}, // never the gh of this machine
		CleanupFetch: func(string, string, time.Duration) error {
			f.fetches++
			return f.fetchErr
		},
		CleanupDown: func(Target) func(io.Writer) error {
			return func(io.Writer) error { f.downs++; return nil }
		},
	}
	return f
}

func (f *cleanupLoopFx) tick(d time.Duration) CleanupStatus {
	f.now = f.now.Add(d)
	f.l.Tick(context.Background())
	return *f.l.Status().Repos[0].Cleanup
}

func (f *cleanupLoopFx) exists() bool { _, err := os.Stat(f.WT); return err == nil }

func TestSuggestShowsTheCandidateAndNeverCleans(t *testing.T) {
	f := newCleanupLoop(t, config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24})
	st := f.tick(0)
	if len(st.Candidates) != 1 || st.Candidates[0].Branch != "feature-login" || st.Candidates[0].State != "merged" ||
		!st.Candidates[0].Eligible || st.Candidates[0].Project != "acme-web-feature-login" || !st.Candidates[0].CleanAt.IsZero() {
		t.Fatalf("%+v", st)
	}
	if st.Candidates[0].Since != f.now {
		t.Fatalf("first eligible time: %v", st.Candidates[0].Since)
	}
	st = f.tick(72 * time.Hour)
	if !f.exists() || f.downs != 0 || len(st.Candidates) != 1 {
		t.Fatal("suggest must never clean without a click")
	}
	if st.Candidates[0].Since.Equal(f.now) {
		t.Fatal("the first-eligible time must not move")
	}
}

func TestAutoStartsTheCleanupWhenTheGraceEnds(t *testing.T) {
	f := newCleanupLoop(t, config.Cleanup{Mode: config.CleanupAuto, GraceHours: 24, DeleteBranch: true})
	st := f.tick(0)
	start := f.now
	if len(st.Candidates) != 1 || !st.Candidates[0].CleanAt.Equal(start.Add(24*time.Hour)) {
		t.Fatalf("grace countdown: %+v", st)
	}
	f.tick(23 * time.Hour)
	if !f.exists() {
		t.Fatal("cleaned before the grace period ended")
	}
	f.tick(30 * time.Minute)
	if !f.exists() {
		t.Fatal("cleaned 30 minutes early")
	}
	st = f.tick(30 * time.Minute) // 24 h after it was first eligible
	if f.exists() || f.downs != 1 || len(st.Candidates) != 0 {
		t.Fatalf("exists=%v downs=%d candidates=%+v", f.exists(), f.downs, st.Candidates)
	}
	list, _ := runs.List(f.Inst.InstanceDir)
	if len(list) != 1 || list[0].Cause == nil || list[0].Cause.Cleanup == nil || list[0].Outcome != runs.Succeeded {
		t.Fatalf("runs: %+v", list)
	}
	if git(t, f.Main, "branch", "--list", "feature-login") != "" {
		t.Error("delete_branch is on: the branch must be gone")
	}
	f.tick(time.Hour)
	if f.downs != 1 {
		t.Fatal("a second cleanup ran")
	}
}

func TestKeepHoldsUntilTheHeadChanges(t *testing.T) {
	f := newCleanupLoop(t, config.Cleanup{Mode: config.CleanupAuto, GraceHours: 1})
	st := f.tick(0)
	c := st.Candidates[0]
	if err := KeepBranch(f.State, f.Main, c.Branch, c.Head); err != nil {
		t.Fatal(err)
	}
	st = f.tick(3 * time.Hour)
	if !f.exists() || len(st.Candidates) != 1 || !st.Candidates[0].Kept || !st.Candidates[0].CleanAt.IsZero() || !st.Candidates[0].Since.IsZero() {
		t.Fatalf("kept: exists=%v %+v", f.exists(), st)
	}
	write(t, f.WT, "more.txt", "x\n") // a new commit changes the head
	git(t, f.WT, "add", "-A")
	git(t, f.WT, "commit", "-q", "-m", "more")
	st = f.tick(3 * time.Hour)
	if !f.exists() || st.Candidates[0].Kept || st.Candidates[0].Eligible {
		t.Fatalf("a new head ends the keep, and the unmerged commit stops the cleanup: %+v", st.Candidates)
	}
}

func TestAnEditDuringTheGraceReplacesTheCountdownWithTheWarning(t *testing.T) {
	f := newCleanupLoop(t, config.Cleanup{Mode: config.CleanupAuto, GraceHours: 24})
	f.tick(0)
	f.tick(23 * time.Hour)
	write(t, f.WT, "login.txt", "edited\n")
	st := f.tick(59 * time.Minute)
	c := st.Candidates[0]
	if c.Eligible || c.Warning != "PR merged, but this worktree has uncommitted changes" || !c.CleanAt.IsZero() {
		t.Fatalf("%+v", c)
	}
	f.tick(2 * time.Hour)
	if !f.exists() || f.downs != 0 {
		t.Fatal("cleaned an edited worktree")
	}
	git(t, f.WT, "checkout", "--", "login.txt")
	st = f.tick(time.Hour)
	if !st.Candidates[0].Eligible || !st.Candidates[0].CleanAt.Equal(f.now.Add(24*time.Hour)) {
		t.Fatalf("the grace period must start again: %+v", st.Candidates[0])
	}
}

func TestNothingIsCheckedWhilePausedOrOff(t *testing.T) {
	f := newCleanupLoop(t, config.Cleanup{Mode: config.CleanupAuto, GraceHours: 1})
	f.tick(0)
	n := f.fetches
	WritePause(f.State, f.Main, Pause{Kind: PauseFailed, Reason: "run 3 failed at step rebuild", At: f.now})
	st := f.tick(5 * time.Hour)
	if f.fetches != n || !st.Paused || len(st.Candidates) != 0 || !f.exists() {
		t.Fatalf("paused: fetches %d->%d %+v", n, f.fetches, st)
	}
	Resume(f.State, f.Main)
	f.cfg.Cleanup[f.Main] = config.Cleanup{Mode: config.CleanupOff, GraceHours: 1}
	n = f.fetches
	st = f.tick(5 * time.Hour)
	if f.fetches != n || st.Mode != "off" || len(st.Candidates) != 0 || !f.exists() {
		t.Fatalf("off: %+v", st)
	}
}

func TestCleanupFetchFollowsTheScheduleAndBacksOff(t *testing.T) {
	f := newCleanupLoop(t, config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24})
	f.tick(0)
	f.tick(time.Minute)
	if f.fetches != 1 {
		t.Fatalf("a second fetch inside 2 minutes: %d", f.fetches)
	}
	f.tick(2 * time.Minute)
	if f.fetches != 2 {
		t.Fatalf("fetches: %d", f.fetches)
	}
	f.fetchErr = errors.New("offline")
	st := f.tick(2 * time.Minute)
	if f.fetches != 3 || st.Fetch != "could not fetch origin; next try in 4 min" || len(st.Candidates) != 1 {
		t.Fatalf("%d %+v", f.fetches, st)
	}
	f.tick(3 * time.Minute)
	if f.fetches != 3 {
		t.Fatal("the backoff was ignored")
	}
}

func TestInspectCleanupReadsAndWritesNothing(t *testing.T) {
	f := newCleanupLoop(t, config.Cleanup{Mode: config.CleanupAuto, GraceHours: 24})
	st := InspectCleanup(f.State, f.Main, f.cfg.Cleanup[f.Main], []Target{f.Inst}, f.now)
	if len(st.Candidates) != 1 || !st.Candidates[0].Eligible || !st.Candidates[0].Since.IsZero() {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(CleanupStateFile(f.State, f.Main)); err == nil {
		t.Fatal("a one-shot status wrote the cleanup state")
	}
}
