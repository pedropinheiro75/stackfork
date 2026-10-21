package live

import (
	"errors"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func finishedRun(t *testing.T, dir, command, trigger string, skip bool, fail ...bool) {
	t.Helper()
	r, err := runs.Begin(dir, runs.Meta{Command: command, Trigger: trigger})
	if err != nil {
		t.Fatal(err)
	}
	s := r.Step("migrate", knowledge.KindExec, "bun run db:migrate  on host")
	if skip {
		s.Skip(HostSkipReason)
	} else {
		s.Start()
		s.End(nil, 0)
	}
	if len(fail) > 0 && fail[0] {
		r.Finish(errors.New("boom"))
		return
	}
	r.Finish(nil)
}

// A live run that only skipped a host step settled the baseline, so the
// change would vanish: the status keeps a warning until a person's full run.
func TestHostStepsSkippedUntilAFullRun(t *testing.T) {
	dir := t.TempDir()
	if HostStepsSkipped(dir) {
		t.Fatal("no run, no warning")
	}
	finishedRun(t, dir, "start-env", "cli", false)
	finishedRun(t, dir, "live", TriggerLive, true)
	if !HostStepsSkipped(dir) {
		t.Fatal("the live run skipped a host step")
	}
	finishedRun(t, dir, "live", TriggerLive, false) // another live run does not run it either
	if !HostStepsSkipped(dir) {
		t.Fatal("a later live run must not hide the skipped host step")
	}
	// only a start-env or reset that completed clears it
	finishedRun(t, dir, "up", "cli", false)
	finishedRun(t, dir, "seed", "cli", false)
	finishedRun(t, dir, "start-env", "cli", false, true)
	if !HostStepsSkipped(dir) {
		t.Fatal("up, seed and a failed start-env ran no host step")
	}
	finishedRun(t, dir, "reset", "cli", false)
	if HostStepsSkipped(dir) {
		t.Fatal("reset ran the host steps")
	}
}
