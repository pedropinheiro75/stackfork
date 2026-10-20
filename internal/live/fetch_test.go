package live

import (
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/repos"
)

func mid() float64 { return 0.5 } // zero jitter

func TestScheduleBackoffAndReset(t *testing.T) {
	s := NewSchedule(mid)
	now := t0
	if !s.Due(now, false) {
		t.Fatal("first fetch not due at once")
	}
	var got []time.Duration
	step := func(ok bool) {
		s.Done(now, ok)
		got = append(got, s.Next().Sub(now))
		now = s.Next()
	}
	step(true)
	for i := 0; i < 5; i++ {
		step(false)
	}
	step(true)
	want := []time.Duration{2, 4, 8, 16, 30, 30, 2}
	for i := range want {
		if got[i] != want[i]*time.Minute {
			t.Fatalf("intervals %v, want %v min", got, want)
		}
	}
}

func TestScheduleJitterBounds(t *testing.T) {
	for _, c := range []struct {
		r    float64
		want time.Duration
	}{{0, 96 * time.Second}, {0.5, 120 * time.Second}, {1, 144 * time.Second}} {
		s := NewSchedule(func() float64 { return c.r })
		s.Done(t0, true)
		if d := s.Next().Sub(t0); d != c.want {
			t.Errorf("rand %v: %v, want %v", c.r, d, c.want)
		}
	}
}

func TestScheduleNotDueWhilePausedOrEarly(t *testing.T) {
	s := NewSchedule(mid)
	s.Done(t0, true)
	if s.Due(t0.Add(119*time.Second), false) || !s.Due(t0.Add(120*time.Second), false) {
		t.Fatal("due boundary")
	}
	if s.Due(t0.Add(time.Hour), true) {
		t.Fatal("due while paused")
	}
}

func TestFetcherWarnsOnceForTwoTicksOnTheSameUpstreamCommit(t *testing.T) {
	calls := 0
	f := &Fetcher{Checkout: "/Users/dev/code/acme-web", Sched: NewSchedule(mid),
		Sync: func(string, time.Duration) repos.SyncResult {
			calls++
			return repos.SyncResult{Behind: 2, Dirty: true, Upstream: "origin/feature-login", UpstreamSHA: "8b07d44", Line: "warning: x"}
		}}
	now := t0
	var warns []string
	for i := 0; i < 2; i++ {
		tk := f.Tick(now, false)
		if !tk.Ran {
			t.Fatalf("tick %d did not run", i)
		}
		if tk.Warn != "" {
			warns = append(warns, tk.Warn)
		}
		now = now.Add(3 * time.Minute)
	}
	if len(warns) != 1 || calls != 2 {
		t.Fatalf("warns %v, calls %d", warns, calls)
	}
	// a new upstream commit warns again
	f.Sync = func(string, time.Duration) repos.SyncResult {
		return repos.SyncResult{Behind: 3, Dirty: true, UpstreamSHA: "9c18e55", Line: "warning: y"}
	}
	if tk := f.Tick(now, false); tk.Warn != "warning: y" {
		t.Fatalf("new commit: %+v", tk)
	}
}

func TestFetcherFailureNoteAndNoFetchWhilePaused(t *testing.T) {
	calls := 0
	f := &Fetcher{Sched: NewSchedule(mid), Sync: func(string, time.Duration) repos.SyncResult {
		calls++
		return repos.SyncResult{FetchFailed: true, Upstream: "origin/feature-login"}
	}}
	if tk := f.Tick(t0, true); tk.Ran || calls != 0 {
		t.Fatal("fetched while paused")
	}
	f.Tick(t0, false)
	tk := f.Tick(t0.Add(4*time.Minute), false) // second failure: next in 8 min
	if want := "could not fetch origin/feature-login; next try in 8 min"; tk.Note != want {
		t.Fatalf("%q, want %q", tk.Note, want)
	}
	if tk := f.Tick(t0.Add(5*time.Minute), false); tk.Ran {
		t.Fatal("ran before the backoff elapsed")
	}
}
