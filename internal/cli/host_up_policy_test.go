//go:build !windows

package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// upRestarts runs `up --mode hybrid` and returns the host apps it started.
func upRestarts(t *testing.T, g []string) []string {
	t.Helper()
	var restarted []string
	old := lifecycle.HostRestart
	lifecycle.HostRestart = func(_ io.Writer, _, _, name, _, _ string) error { restarted = append(restarted, name); return nil }
	defer func() { lifecycle.HostRestart = old }() // a later `host restart` in the test needs the real one
	if _, stderr, code := runCLI(t, append(g, "up", "--mode", "hybrid", "--wait-timeout", "5")...); code != 0 {
		t.Fatalf("up: %d %s", code, stderr)
	}
	return restarted
}

// deadAfterForeground makes the entry of a finished foreground run look like
// a real one: runCLI runs in this process, which outlives the command.
func deadAfterForeground(t *testing.T, stateDir, project, name string) {
	t.Helper()
	m := readManifestAt(t, stateDir, project)
	p := m.HostProcs[name]
	p.PID, p.StartedAt = 999999, "1970-01-01T00:00:00Z"
	m.HostProcs[name] = p
	if err := state.WritePrivate(filepath.Join(stateDir, project, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	rec, err := state.ReadHostRecord(filepath.Join(stateDir, project), name)
	if err != nil || rec == nil {
		t.Fatalf("record: %v", err)
	}
	rec.SupervisorPID = 999999
	if err := state.WriteHostRecord(filepath.Join(stateDir, project), *rec); err != nil {
		t.Fatal(err)
	}
}

func TestUpDoesNotRerunAForegroundOneShot(t *testing.T) {
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n") // running, so `host` accepts it
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "reset", "--", "sh", "-c", "exit 0")...); code != 0 {
		t.Fatalf("one-shot: %d %s", code, stderr)
	}
	deadAfterForeground(t, stateDir, project, "reset")
	t.Setenv("DOCKER_FAKE_PS", "")
	if got := upRestarts(t, g); len(got) != 0 {
		t.Fatalf("a one-shot command that exited 0 must not run again on up: %v", got)
	}
}

func TestUpRestartsAServerKilledBySIGHUP(t *testing.T) {
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	t.Setenv("STACKFORK_TEST_HELPER", "1")
	t.Setenv("HELPER_LISTEN_AFTER", "100ms")
	g := []string{"--root", root, "--state-dir", stateDir}
	dir := filepath.Join(stateDir, project)
	go func() { // once it listens, hang it up: the foreground run forwards SIGHUP to its child
		for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if r, _ := state.ReadHostRecord(dir, "web"); r != nil && r.Listened {
				syscall.Kill(os.Getpid(), syscall.SIGHUP)
				return
			}
		}
	}()
	runCLI(t, append(g, "host", "web", "--", os.Args[0])...)
	rec, _ := state.ReadHostRecord(dir, "web")
	if rec == nil || !rec.Listened || rec.ExitCode == nil || *rec.ExitCode == 0 {
		t.Fatalf("a server ended by a signal should record listened and a non-zero code: %+v", rec)
	}
	deadAfterForeground(t, stateDir, project, "web")
	t.Setenv("DOCKER_FAKE_PS", "")
	if got := upRestarts(t, g); len(got) != 1 || got[0] != "web" {
		t.Fatalf("up must restart a server a signal ended: %v", got)
	}
}

func TestUpLeavesAnAppAPersonStopped(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	t.Setenv("STACKFORK_TEST_HELPER", "1")
	t.Setenv("HELPER_LISTEN_AFTER", "100ms")
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "web", "--wait-timeout", "20s", "--", os.Args[0])...); code != 0 {
		t.Fatalf("start: %d %s", code, stderr)
	}
	dir := filepath.Join(stateDir, project)
	if r, _ := state.ReadHostRecord(dir, "web"); r == nil || !r.Listened {
		t.Fatalf("a server records that it listened: %+v", r)
	}
	if _, stderr, code := runCLI(t, append(g, "host", "stop", "web")...); code != 0 {
		t.Fatalf("stop: %d %s", code, stderr)
	}
	if r, _ := state.ReadHostRecord(dir, "web"); r == nil || !r.StoppedByPerson {
		t.Fatalf("host stop must record stopped_by_person: %+v", r)
	}
	ps := os.Getenv("DOCKER_FAKE_PS")
	t.Setenv("DOCKER_FAKE_PS", "")
	if got := upRestarts(t, g); len(got) != 0 {
		t.Fatalf("up must leave a stopped app stopped: %v", got)
	}
	t.Setenv("DOCKER_FAKE_PS", ps)
	// an explicit restart still works and clears the flag
	if _, stderr, code := runCLI(t, append(g, "host", "restart", "web")...); code != 0 {
		t.Fatalf("restart: %d %s", code, stderr)
	}
	if r, _ := state.ReadHostRecord(dir, "web"); r == nil || r.StoppedByPerson {
		t.Fatalf("restart clears stopped_by_person: %+v", r)
	}
}

// A worker that never listens is not killed: the start warns and leaves it running.
func TestHostStartTimeoutWarnsAndLeavesTheAppRunning(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	t.Setenv("STACKFORK_HOST_WAIT_TIMEOUT", "")
	g := []string{"--root", root, "--state-dir", stateDir}
	_, stderr, code := runCLI(t, append(g, "host", "start", "worker", "--port", "31778", "--wait-timeout", "1s", "--", "sleep", "30")...)
	if code != 0 {
		t.Fatalf("a worker that never listens is not an error: %d %s", code, stderr)
	}
	for _, w := range []string{"worker is running but not listening on 31778 after 1s", "may not use PORT"} {
		if !strings.Contains(stderr, w) {
			t.Errorf("warning lacks %q:\n%s", w, stderr)
		}
	}
	proc := readManifestAt(t, stateDir, project).HostProcs["worker"]
	if !state.HostIdentity(proc) {
		t.Fatalf("the worker must keep running: %+v", proc)
	}
	if readManifestAt(t, stateDir, project).Routes["worker"] == 0 {
		t.Errorf("a running app keeps its route")
	}
}

func TestHostStartExplicitZeroWaitReturnsAtOnceAndIsRemembered(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	t.Setenv("STACKFORK_HOST_WAIT_TIMEOUT", "") // the default would wait 90 s
	g := []string{"--root", root, "--state-dir", stateDir}
	begin := time.Now()
	if _, stderr, code := runCLI(t, append(g, "host", "start", "worker", "--wait-timeout", "0", "--", "sleep", "30")...); code != 0 || time.Since(begin) > 15*time.Second {
		t.Fatalf("--wait-timeout 0 means no wait: %d after %v: %s", code, time.Since(begin), stderr)
	}
	p := readManifestAt(t, stateDir, project).HostProcs["worker"]
	if args := strings.Join(p.RestartArgs(), " "); !strings.Contains(args, "--wait-timeout 0") {
		t.Errorf("restart and up must keep the wait: %q", args)
	}
	if w, set := waitFromArgs(p.RestartArgs()); !set || w != 0 {
		t.Errorf("waitFromArgs = %v %v", w, set)
	}
}

// The error of a start that failed carries the redacted log, never a secret.
func TestHostStartFailureShowsOnlyRedactedLog(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	t.Setenv("HOSTFAIL_TOKEN", "canary-0123456789abcdef")
	mp := filepath.Join(stateDir, project, "manifest.json")
	m := readManifestAt(t, stateDir, project)
	m.Secrets = map[string]string{"api_token": "env:HOSTFAIL_TOKEN"}
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	g := []string{"--root", root, "--state-dir", stateDir}
	_, stderr, code := runCLI(t, append(g, "host", "start", "crash", "--", "sh", "-c", "echo token=canary-0123456789abcdef; exit 3")...)
	if code == 0 {
		t.Fatal("a command that exits at once must fail the start")
	}
	if strings.Contains(stderr, "canary-0123456789abcdef") || !strings.Contains(stderr, "[redacted:api_token]") {
		t.Errorf("error text:\n%s", stderr)
	}
}

func TestHostStartRecordsDirRelativeToTheRoot(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	sub := filepath.Join(root, "web")
	os.MkdirAll(sub, 0o755)
	t.Chdir(sub)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "ui", "--", "sleep", "30")...); code != 0 {
		t.Fatalf("start: %d %s", code, stderr)
	}
	p := readManifestAt(t, stateDir, project).HostProcs["ui"]
	if args := strings.Join(p.RestartArgs(), " "); !strings.Contains(args, "--dir web") {
		t.Errorf("restart args must carry --dir web: %q", args)
	}
}
