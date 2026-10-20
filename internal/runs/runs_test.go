package runs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func readRun(t *testing.T, dir string, id int) *Run {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "runs", itoa(id)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestRoundTripEveryState(t *testing.T) {
	dir := t.TempDir()
	r, err := newRun(dir, Meta{Command: "start-env", Trigger: "cli", Mode: "hybrid", Project: "p"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	pend := r.Step("pending", "exec", "never reached")
	_ = pend
	run := r.Step("running", "exec", "running")
	run.Start()
	ok := r.Step("ok", "prepare", "ok")
	ok.Start()
	ok.End(nil, 0)
	bad := r.Step("bad", "exec", "bad")
	bad.Start()
	bad.End(errors.New("boom"), 3)
	sk := r.Step("sk", "exec", "skip")
	sk.Skip("data already exists")

	got := readRun(t, dir, 1)
	if got.Schema != 1 || got.Outcome != Running || got.Command != "start-env" || got.Trigger != "cli" {
		t.Fatalf("header = %+v", got)
	}
	want := map[string]string{"pending": Pending, "running": StepRunning, "ok": Succeeded, "bad": Failed, "sk": Skipped}
	for _, s := range got.Steps {
		if s.State != want[s.ID] {
			t.Errorf("step %s = %s, want %s", s.ID, s.State, want[s.ID])
		}
	}
	if got.Steps[3].ExitCode == nil || *got.Steps[3].ExitCode != 3 {
		t.Errorf("exit code = %v", got.Steps[3].ExitCode)
	}
	if got.Steps[4].Reason != "data already exists" || got.Steps[4].StartedAt.IsZero() {
		t.Errorf("skipped = %+v", got.Steps[4])
	}
	if !got.Steps[0].StartedAt.IsZero() {
		t.Error("pending step must have no times")
	}

	for _, out := range []struct {
		err  error
		want string
	}{{nil, Succeeded}, {errors.New("step bad failed"), Failed}} {
		r, _ := newRun(dir, Meta{Command: "up"}, 10+len(out.want))
		r.Finish(out.err)
		got = readRun(t, dir, r.ID)
		if got.Outcome != out.want || got.EndedAt.IsZero() {
			t.Errorf("outcome = %s ended %v, want %s", got.Outcome, got.EndedAt, out.want)
		}
		if out.err != nil && got.Error != out.err.Error() {
			t.Errorf("error = %q", got.Error)
		}
	}
}

func TestModes(t *testing.T) {
	dir := t.TempDir()
	r, _ := newRun(dir, Meta{Command: "up"}, 1)
	s := r.Step("seed", "exec", "x")
	s.Start().Write([]byte("hello\n"))
	s.End(nil, 0)
	for _, p := range []string{"runs/1.json", "runs/1/seed.log"} {
		fi, err := os.Stat(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", p, fi.Mode().Perm())
		}
	}
	for _, p := range []string{"runs", "runs/1"} {
		fi, _ := os.Stat(filepath.Join(dir, p))
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %v", p, fi.Mode().Perm())
		}
	}
}

func TestSeparatorInStepIDFallsBack(t *testing.T) {
	dir := t.TempDir()
	r, _ := newRun(dir, Meta{Command: "up"}, 1)
	r.Step("first", "exec", "x")
	s := r.Step("a/b", "exec", "x")
	s.Start().Write([]byte("hi"))
	s.End(nil, 0)
	if s.Log != "1/step-2.log" {
		t.Fatalf("log = %q", s.Log)
	}
	if _, err := os.Stat(filepath.Join(dir, "runs", "1", "step-2.log")); err != nil {
		t.Fatal(err)
	}
}
