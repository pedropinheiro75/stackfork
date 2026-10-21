package live

import (
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func finishedRun(t *testing.T, dir, command, trigger string, skip bool) {
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
	finishedRun(t, dir, "start-env", "cli", false)
	if HostStepsSkipped(dir) {
		t.Fatal("start-env ran the host steps")
	}
}
