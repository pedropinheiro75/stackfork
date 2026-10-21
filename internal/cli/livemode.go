package cli

import (
	"context"
	"io"

	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/runs"
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
