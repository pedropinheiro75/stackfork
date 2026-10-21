package live

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// Fetch schedule (design D8).
const (
	FetchInterval = 2 * time.Minute
	FetchTimeout  = 20 * time.Second
	FetchBackoff  = 30 * time.Minute
	FetchJitter   = 0.2
)

// Schedule says when the next fetch is due: Interval with +-Jitter, doubling
// on each consecutive failure up to Max, back to Interval after a success.
// The same schedule drives branch-cleanup fetches (design D16).
type Schedule struct {
	Interval, Max time.Duration
	Jitter        float64
	Rand          func() float64 // [0,1); nil uses math/rand
	fails         int
	next          time.Time
}

// NewSchedule uses the design's 2 min, 30 min and 20 %.
func NewSchedule(rnd func() float64) *Schedule {
	return &Schedule{Interval: FetchInterval, Max: FetchBackoff, Jitter: FetchJitter, Rand: rnd}
}

// Due reports whether a fetch should run now. Never while paused.
func (s *Schedule) Due(now time.Time, paused bool) bool {
	return !paused && !now.Before(s.next)
}

// Next is when the next fetch is due.
func (s *Schedule) Next() time.Time { return s.next }

// Done records a fetch that ended at now and sets the next due time.
func (s *Schedule) Done(now time.Time, ok bool) {
	if ok {
		s.fails = 0
	} else {
		s.fails++
	}
	d := s.Interval
	for i := 0; i < s.fails && d < s.Max; i++ {
		d *= 2
	}
	if d > s.Max {
		d = s.Max
	}
	r := s.Rand
	if r == nil {
		r = rand.Float64
	}
	d = time.Duration(float64(d) * (1 + s.Jitter*(2*r()-1)))
	s.next = now.Add(d)
}

// Fetcher runs Sync for one checkout on a Schedule and keeps the
// once-per-upstream-commit rule for warnings.
type Fetcher struct {
	Checkout string
	Sched    *Schedule
	Sync     func(checkout string, timeout time.Duration) repos.SyncResult // nil uses repos.SyncUnattended
	warned   string
}

// FetchTick is the outcome of one Fetcher.Tick.
type FetchTick struct {
	Ran    bool
	Result repos.SyncResult
	// Warn is the blocked-fast-forward warning, once for each upstream commit.
	Warn string
	// Note is the failed-fetch text with the next try, "" when the fetch worked.
	Note string
}

// Tick fetches when it is due and not paused.
func (f *Fetcher) Tick(now time.Time, paused bool) FetchTick {
	if !f.Sched.Due(now, paused) {
		return FetchTick{}
	}
	sync := f.Sync
	if sync == nil {
		sync = repos.SyncUnattended
	}
	res := sync(f.Checkout, FetchTimeout)
	f.Sched.Done(now, !res.FetchFailed)
	out := FetchTick{Ran: true, Result: res}
	switch {
	case res.FetchFailed:
		wait := f.Sched.Next().Sub(now).Round(time.Minute)
		out.Note = fmt.Sprintf("could not fetch %s; next try in %d min", res.Upstream, int(wait.Minutes()))
	case !res.Updated && res.Behind > 0 && res.UpstreamSHA != f.warned:
		f.warned = res.UpstreamSHA
		out.Warn = res.Line
	}
	return out
}
