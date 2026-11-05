package runs

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadersDeriveInterrupted(t *testing.T) {
	dir := t.TempDir()
	r, _ := newRun(dir, Meta{Command: "start-env"}, 1)
	startAbandoned(t, r.Step("a", "exec", "x"))
	r.Step("b", "exec", "x")

	got, err := Get(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != Interrupted || got.Steps[0].State != Failed || got.Steps[0].Reason != "interrupted" {
		t.Fatalf("got %s %+v", got.Outcome, got.Steps[0])
	}
	if !got.EndedAt.IsZero() {
		t.Error("ended_at must stay unknown")
	}
	if stored := readRun(t, dir, 1); stored.Outcome != Running {
		t.Error("a reader must not rewrite the file")
	}
	list, _ := List(dir)
	if len(list) != 1 || list[0].Outcome != Interrupted {
		t.Fatalf("list = %+v", list)
	}
}

func TestListNewestFirstAndRunningWithLock(t *testing.T) {
	dir := t.TempDir()
	a, _ := Begin(dir, Meta{Command: "up"})
	s := a.Step("s", "exec", "x")
	s.Start().Write([]byte("line\n"))
	s.End(nil, 0)
	a.Finish(nil)
	b, _ := Begin(dir, Meta{Command: "seed"})
	defer b.Finish(nil)
	list, err := List(dir)
	if err != nil || len(list) != 2 || list[0].ID != 2 || list[0].Outcome != Running {
		t.Fatalf("list = %v %v", list, err)
	}
	if list[1].Steps[0].DurationMS < 0 || list[1].DurationMS < 0 {
		t.Error("negative duration")
	}
	if l, _ := List(t.TempDir()); len(l) != 0 {
		t.Error("empty dir must list nothing")
	}
	log, err := Log(dir, 1, "s")
	if err != nil || string(log) != "line\n" {
		t.Fatalf("log = %q %v", log, err)
	}
	if _, err := Log(dir, 1, "../x"); !os.IsNotExist(err) && err == nil {
		t.Error("unknown step must fail")
	}
	if _, err := Get(dir, 9); err == nil {
		t.Error("unknown id must fail")
	}
}

func TestHelperMidStep(t *testing.T) {
	dir := os.Getenv("RUNS_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process only")
	}
	r, err := Begin(dir, Meta{Command: "start-env"})
	if err != nil {
		os.Exit(2)
	}
	r.Step("seed", "exec", "x").Start()
	os.Stdout.WriteString("ready\n")
	time.Sleep(time.Minute)
}

func TestKilledProcessReadsInterrupted(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperMidStep$")
	cmd.Env = append(os.Environ(), "RUNS_HELPER_DIR="+dir)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	line, _ := bufio.NewReader(out).ReadString('\n')
	if !strings.HasPrefix(line, "ready") {
		cmd.Process.Kill()
		t.Fatalf("helper said %q", line)
	}
	got, _ := Get(dir, 1)
	if got.Outcome != Running {
		t.Fatalf("live helper reads %s", got.Outcome)
	}
	cmd.Process.Kill()
	cmd.Wait()
	got, _ = Get(dir, 1)
	if got.Outcome != Interrupted || got.Steps[0].State != Failed || got.Steps[0].Reason != "interrupted" {
		t.Fatalf("after kill: %s %+v", got.Outcome, got.Steps[0])
	}
}

// Log trusts the stored run only when its log path is the one the writer
// would have made, and never follows a symlink.
func TestLogRefusesTamperedPathAndSymlink(t *testing.T) {
	dir := t.TempDir()
	r, _ := Begin(dir, Meta{Command: "up"})
	s := r.Step("s", "exec", "x")
	s.Start().Write([]byte("line\n"))
	s.End(nil, 0)
	r.Finish(nil)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("private"), 0o600)

	path := filepath.Join(runsDir(dir), "1.json")
	data, _ := os.ReadFile(path)
	rel, _ := filepath.Rel(runsDir(dir), outside)
	os.WriteFile(path, []byte(strings.Replace(string(data), `"1/s.log"`, `"`+filepath.ToSlash(rel)+`"`, 1)), 0o600)
	if b, err := Log(dir, 1, "s"); err == nil {
		t.Fatalf("a log path outside <id>/<step>.log was read: %q", b)
	}

	os.WriteFile(path, data, 0o600) // restore the stored path, then swap the file for a link
	logf := filepath.Join(runsDir(dir), "1", "s.log")
	os.Remove(logf)
	if err := os.Symlink(outside, logf); err != nil {
		t.Skip("no symlinks:", err)
	}
	if b, err := Log(dir, 1, "s"); err == nil {
		t.Fatalf("a symlinked log was read: %q", b)
	}
}

// startAbandoned starts s as a run that died would leave it, and closes its
// log when the test ends: Windows cannot remove an open file.
func startAbandoned(t *testing.T, s *Step) {
	t.Helper()
	s.Start()
	w := s.w
	t.Cleanup(func() { w.closeWith("") })
}
