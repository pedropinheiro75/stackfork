package live

import (
	"context"
	"fmt"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
)

// cleanupLoop is the per-repository state of the cleanup part of the loop.
type cleanupLoop struct {
	sched     *Schedule
	cands     []CandidateStatus
	fetchNote string
	lastFetch time.Time
}

func (l *Loop) gh() *cleanup.GH {
	if l.GH == nil {
		l.GH = &cleanup.GH{RefuseUnder: l.cfgMains()}
		l.GH.Now = l.Now
	}
	return l.GH
}

func (l *Loop) cfgMains() []string {
	if l.cfg == nil {
		return nil
	}
	return l.cfg.LiveRepos()
}

// tickCleanup is the branch-cleanup pass of one repository (design D15-D20):
// on the fetch schedule it prunes the default branch's remote and classifies
// the linked worktrees; every tick it starts the cleanup of an `auto`
// candidate whose grace period ended. Nothing is checked while the
// repository is paused or cleanup is off.
func (l *Loop) tickCleanup(ctx context.Context, rs *repoState, main string, cl config.Cleanup, paused bool, now time.Time) *CleanupStatus {
	st := &CleanupStatus{Mode: cl.Mode, GraceHours: cl.GraceHours, DeleteBranch: cl.DeleteBranch, Candidates: []CandidateStatus{}}
	if cl.Mode == config.CleanupOff {
		rs.cl = cleanupLoop{}
		return st
	}
	if paused {
		st.Paused = true
		return st
	}
	c := &rs.cl
	if c.sched == nil {
		c.sched = NewSchedule(l.Rand)
	}
	in := ScanInput{Main: main, NewBase: l.NewBase, Settings: cl, Targets: rs.targets, Now: now, GH: l.gh(), ActionLocked: l.ActionLocked, Persist: true}
	if c.sched.Due(now, false) {
		fetch := l.CleanupFetch
		if fetch == nil {
			fetch = cleanup.Fetch
		}
		remote := cleanup.Remote(main)
		err := fetch(main, remote, FetchTimeout)
		c.sched.Done(now, err == nil)
		c.lastFetch = now
		if err != nil {
			wait := c.sched.Next().Sub(now).Round(time.Minute)
			c.fetchNote = fmt.Sprintf("could not fetch %s; next try in %d min", remote, int(wait.Minutes()))
		} else {
			c.fetchNote = ""
			c.cands, _ = ScanCleanup(in)
		}
	}
	st.Fetch, st.LastFetch = c.fetchNote, c.lastFetch

	if cl.Mode == config.CleanupAuto {
		for _, cand := range c.cands {
			if cand.State != cleanup.Merged || !cand.Eligible || cand.Kept || cand.CleanAt.IsZero() || now.Before(cand.CleanAt) {
				continue
			}
			l.autoClean(ctx, in, cand)
			c.cands, _ = ScanCleanup(in) // local refs: the worktree is gone or a warning replaces the countdown
			break                        // one cleanup at a time
		}
	}
	st.Candidates = append(st.Candidates, c.cands...)
	return st
}

// autoClean runs the cleanup of one candidate whose grace period ended. The
// run re-checks eligibility itself; a failed re-check runs nothing.
func (l *Loop) autoClean(ctx context.Context, in ScanInput, c CandidateStatus) {
	release, _, ok := l.rec.Slot(l.NewBase, in.Main)
	if !ok {
		return
	}
	j := in.Job(c, true)
	if j.Instance != nil && l.CleanupDown != nil {
		j.Down = l.CleanupDown(*j.Instance)
	}
	j.Now = l.Now
	l.inflight.Add(1)
	l.busyMain.Store(in.Main)
	run := l.CleanupExec
	if run == nil {
		run = RunCleanup
	}
	out := run(ctx, j)
	l.busyMain.Store("")
	l.inflight.Add(-1)
	release(out.Run.ID != 0 || out.Err == nil)
}
