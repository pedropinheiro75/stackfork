package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// LiveExec is the live.Exec of the dashboard's reconciler: it runs a Job as
// one recorded live run through StartEnv (command live, trigger live, run
// lock tried once, `secrets` first, no host step, no data removal). out gets
// the step output; nil discards it (the step logs keep it). The result names
// the run so the reconciler can move the baseline or pause.
func LiveExec(newBase, legacyBase string, profileDirs []string, out io.Writer) live.Exec {
	if out == nil {
		out = io.Discard
	}
	return func(_ context.Context, t live.Target, j live.Job) (live.RunResult, error) {
		before := 0
		if r := runs.Latest(t.InstanceDir); r != nil {
			before = r.ID
		}
		id := 0
		err := StartEnv(out, out, StartOptions{
			Checkout: t.Root, NewStateBase: newBase, LegacyStateBase: legacyBase, Global: t.Global, ProfileDirs: profileDirs,
			Mode: j.Mode, Live: &j,
			Observer: func(e runs.Event) {
				if id == 0 {
					id = e.Run
				}
			},
		})
		if id == 0 { // a run that began and ended before any event
			if r := runs.Latest(t.InstanceDir); r != nil && r.ID > before {
				id = r.ID
			}
		}
		res := live.RunResult{ID: id}
		if id != 0 {
			if r, gerr := runs.Get(t.InstanceDir, id); gerr == nil {
				res.Outcome, res.Step = r.Outcome, r.FailedStep()
			}
		}
		return res, err
	}
}

// liveLoopSetup lets a test replace the loop's Exec, Targets and clock.
var liveLoopSetup func(*live.Loop)

// startLiveLoop starts the dashboard's reconciler (add-live-mode D2). The
// loop is idle, and holds no lock, until a repository has live mode on. stop
// ends it; done closes once it has, which is after a live run in progress.
func startLiveLoop(newBase, legacyBase string, profiles []string, srv *dashboard.Server) (loop *live.Loop, stop context.CancelFunc, done chan struct{}) {
	loop = &live.Loop{NewBase: newBase, LegacyBase: legacyBase, Version: Version,
		Exec: LiveExec(newBase, legacyBase, profiles, nil), ActionLocked: srv.ActionLocked,
		CleanupDown: cleanupDown(newBase, legacyBase)}
	if liveLoopSetup != nil {
		liveLoopSetup(loop)
	}
	ctx, stop := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { defer close(done); loop.Run(ctx) }()
	return loop, stop, done
}

// heldLock is state.Held under a name the live commands share.
func heldLock(path string) bool { return state.Held(path) }

// dashboardLive backs the page's Live switch (add-live-mode D14). The loop
// does not exist yet when the server is built, so Status reads it through a
// pointer the dashboard command fills once the loop runs.
func dashboardLive(newBase, legacyBase string, loop *atomic.Pointer[live.Loop]) *dashboard.LiveOps {
	return &dashboard.LiveOps{
		Status: func() live.Status {
			if l := loop.Load(); l != nil {
				return l.Status()
			}
			return live.Status{}
		},
		Prepare: func(main string) error {
			_, _, err := liveOnCheck(main, newBase, legacyBase)
			return err
		},
		Enable: func(main string) error { return liveOnWrite(main, newBase) },
		Resume: func(main string) error {
			var plan knowledge.Plan
			k, _, err := knowledge.LoadFor(main, main)
			if err != nil {
				return err
			}
			if k != nil {
				plan = k.Plan
			}
			if ok, _ := lifecycle.HashTrusted(newBase, main, plan); !ok {
				return errors.New("Plan changed; host steps need trust:\n" + strings.Join(lifecycle.ReadableDiff(newBase, main, plan), "\n"))
			}
			return live.Resume(newBase, main)
		},
	}
}
