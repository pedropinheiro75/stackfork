package runs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConcurrentBeginNamesOwner(t *testing.T) {
	lockWait = 100 * time.Millisecond
	dir := t.TempDir()
	a, err := Begin(dir, Meta{Command: "start-env", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Begin(dir, Meta{Command: "up", Trigger: "dashboard"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if !strings.Contains(err.Error(), "run 1, start-env from cli") {
		t.Errorf("err = %q", err)
	}
	if _, e := os.Stat(filepath.Join(dir, "runs", "2.json")); e == nil {
		t.Error("refused Begin must write nothing")
	}
	a.Finish(nil)
	b, err := Begin(dir, Meta{Command: "up"})
	if err != nil || b.ID != 2 {
		t.Fatalf("after Finish: %v id %v", err, b)
	}
	b.Finish(nil)
}

func TestRetentionKeepsTwenty(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 21; i++ {
		r, err := Begin(dir, Meta{Command: "up"})
		if err != nil {
			t.Fatal(err)
		}
		if r.ID != i {
			t.Fatalf("id = %d, want %d", r.ID, i)
		}
		s := r.Step("s", "exec", "x")
		s.Start().Write([]byte("x"))
		s.End(nil, 0)
		r.Finish(nil)
	}
	if _, err := os.Stat(filepath.Join(dir, "runs", "1.json")); !os.IsNotExist(err) {
		t.Error("run 1 must be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "runs", "1")); !os.IsNotExist(err) {
		t.Error("run 1 log dir must be gone")
	}
	for _, p := range []string{"2.json", "21.json", "21/s.log"} {
		if _, err := os.Stat(filepath.Join(dir, "runs", p)); err != nil {
			t.Error(err)
		}
	}
}

func TestBeginSweepsDeadRunning(t *testing.T) {
	dir := t.TempDir()
	// A run that was never finished and whose lock is free: process died.
	dead, _ := newRun(dir, Meta{Command: "start-env"}, 1)
	startAbandoned(t, dead.Step("a", "exec", "x"))
	dead.Step("b", "exec", "x")

	r, err := Begin(dir, Meta{Command: "up"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Finish(nil)
	if r.ID != 2 {
		t.Fatalf("id = %d", r.ID)
	}
	got := readRun(t, dir, 1)
	if got.Outcome != Interrupted || got.EndedAt.IsZero() {
		t.Fatalf("outcome = %s ended %v", got.Outcome, got.EndedAt)
	}
	if got.Steps[0].State != Failed || got.Steps[0].Reason != "interrupted" || got.Steps[1].State != Pending {
		t.Errorf("steps = %+v %+v", got.Steps[0], got.Steps[1])
	}
}
