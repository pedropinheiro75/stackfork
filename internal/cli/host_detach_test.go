//go:build !windows

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

var (
	builtOnce sync.Once
	builtPath string
	builtErr  error
)

// useRealBinary points the detached supervisor at a real stackfork binary:
// os.Executable() inside `go test` is the test binary, not the CLI.
func useRealBinary(t *testing.T) {
	t.Helper()
	builtOnce.Do(func() {
		dir, err := os.MkdirTemp("", "stackfork-bin-")
		if err != nil {
			builtErr = err
			return
		}
		builtPath = filepath.Join(dir, "stackfork")
		out, err := exec.Command("go", "build", "-o", builtPath, "../../cmd/stackfork").CombinedOutput()
		if err != nil {
			builtErr = err
			builtPath = string(out)
		}
	})
	if builtErr != nil {
		t.Fatalf("build stackfork: %v %s", builtErr, builtPath)
	}
	old := hostExecutable
	hostExecutable = func() (string, error) { return builtPath, nil }
	t.Cleanup(func() { hostExecutable = old })
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitDead(pid int, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if !alive(pid) {
			return true
		}
	}
	return !alive(pid)
}

// killOnCleanup guarantees no orphan: whatever HostProcs lists is killed (group) when the test ends.
func killOnCleanup(t *testing.T, stateDir, project string) {
	t.Cleanup(func() {
		m, _ := state.ReadManifest(filepath.Join(stateDir, project, "manifest.json"))
		if m == nil {
			return
		}
		for _, p := range m.HostProcs {
			switch {
			case state.HostIdentity(p):
				state.KillGroup(p.PID, syscall.SIGKILL)
			case !state.HostAlive(p):
				state.KillOrphanGroup(p.PID) // leader gone: only its leftovers can carry that group id
			}
		}
	})
}

func hostEnv(t *testing.T) (root, stateDir, project string) {
	t.Helper()
	useRealBinary(t)
	t.Setenv("STACKFORK_PORT_RANGE_START", "31500")
	t.Setenv("STACKFORK_PORT_RANGE_END", "31999")
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	killOnCleanup(t, stateDir, project)
	return root, stateDir, project
}

func TestHostStartDetachesAndSurvivesCaller(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	begin := time.Now()
	_, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sh", "-c", "while :; do sleep 1; done")...)
	if code != 0 || time.Since(begin) > 5*time.Second {
		t.Fatalf("start: code=%d after %v: %s", code, time.Since(begin), stderr)
	}
	m := readManifestAt(t, stateDir, project)
	proc, ok := m.HostProcs["app"]
	if !ok || !alive(proc.PID) {
		t.Fatalf("HostProcs[app] missing or dead: %+v", m.HostProcs)
	}
	if pg, _ := syscall.Getpgid(proc.PID); pg != proc.PID {
		t.Fatalf("supervisor must lead its own group, pgid=%d pid=%d", pg, proc.PID)
	}
	if _, err := os.Stat(filepath.Join(stateDir, project, "host-app.log")); err != nil {
		t.Fatalf("log: %v", err)
	}
	if m.Routes["app"] == 0 {
		t.Fatalf("route not registered: %+v", m.Routes)
	}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sleep", "5")...); code == 0 || !strings.Contains(stderr, "already running") {
		t.Fatalf("second start must be refused: code=%d %s", code, stderr)
	}

	if _, stderr, code := runCLI(t, append(g, "host", "stop", "app")...); code != 0 {
		t.Fatalf("stop: %d %s", code, stderr)
	}
	if !waitDead(proc.PID, 2*time.Second) {
		t.Fatal("supervisor still alive after stop")
	}
	m = readManifestAt(t, stateDir, project)
	if _, ok := m.Routes["app"]; ok {
		t.Fatal("route must be gone")
	}
	if _, ok := m.HostProcs["app"]; ok {
		t.Fatal("HostProcs must be gone")
	}
}

func TestHostStopEscalatesToKill(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	t.Setenv("STACKFORK_HOST_STOP_TIMEOUT", "1s")
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sh", "-c", `trap "" TERM; while :; do sleep 1; done`)...); code != 0 {
		t.Fatalf("start: %d %s", code, stderr)
	}
	pid := readManifestAt(t, stateDir, project).HostProcs["app"].PID
	begin := time.Now()
	if _, stderr, code := runCLI(t, append(g, "host", "stop", "app")...); code != 0 {
		t.Fatalf("stop: %d %s", code, stderr)
	}
	if time.Since(begin) > 3*time.Second || !waitDead(pid, time.Second) {
		t.Fatalf("stop took %v, alive=%v", time.Since(begin), alive(pid))
	}
}

func TestHostStatusIgnoresRecycledPID(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	instDir := filepath.Join(stateDir, project)
	m := readManifestAt(t, stateDir, project)
	m.HostProcs = map[string]state.HostProc{"app": {PID: os.Getpid(), StartedAt: "1970-01-01T00:00:00Z", Cmdline: os.Args, Port: 31999}}
	m.Routes = map[string]int{"app": 31999}
	if err := state.WritePrivate(filepath.Join(instDir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if running, _ := state.HostStatus(instDir, "app"); running {
		t.Fatal("a recycled PID must not count as running")
	}
	if _, stderr, code := runCLI(t, append(g, "host", "stop", "app")...); code != 0 {
		t.Fatalf("stop: %d %s", code, stderr)
	}
	if !alive(os.Getpid()) {
		t.Fatal("unreachable")
	}
	m = readManifestAt(t, stateDir, project)
	if _, ok := m.HostProcs["app"]; ok {
		t.Fatal("stale entry must be cleaned")
	}
	if _, ok := m.Routes["app"]; ok {
		t.Fatal("stale route must be cleaned")
	}
}

func TestDownStopsHostProcessesFirst(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	logPath := installFakeDocker(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sh", "-c", "while :; do sleep 1; done")...); code != 0 {
		t.Fatalf("start: %d %s", code, stderr)
	}
	pid := readManifestAt(t, stateDir, project).HostProcs["app"].PID
	os.Truncate(logPath, 0)
	if _, stderr, code := runCLI(t, append(g, "down")...); code != 0 {
		t.Fatalf("down: %d %s", code, stderr)
	}
	if alive(pid) {
		t.Fatal("host process must be dead after down")
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), " down ") {
		t.Fatalf("compose down not run: %s", log)
	}
	if m := readManifestAt(t, stateDir, project); len(m.HostProcs) != 0 {
		t.Fatalf("HostProcs left: %+v", m.HostProcs)
	}
}

func TestHostStopRefusesUnknownName(t *testing.T) {
	root, stateDir, _ := hostEnv(t)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "stop", "nope")
	if code != 1 || !strings.Contains(stderr, "no host process nope") {
		t.Fatalf("code=%d %q", code, stderr)
	}
}

// SIGTERM to stackfork is forwarded to the command, and the route is still
// removed afterwards.
func TestHostForwardsSignalsAndCleansUp(t *testing.T) {
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")

	var out, errBuf bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"--root", root, "--state-dir", stateDir, "host", "app", "--", "sh", "-c", "sleep 30"}, &out, &errBuf)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		m, _ := state.ReadManifest(filepath.Join(stateDir, project, "manifest.json"))
		if m != nil && m.Routes["app"] != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("route never registered; stderr=%s", errBuf.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // let cmd.Start finish
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	select {
	case code := <-done:
		if code != 128+int(syscall.SIGTERM) {
			t.Fatalf("expected exit 143 after forwarding SIGTERM, got %d (%s)", code, errBuf.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit after SIGTERM")
	}
	if m := readManifestAt(t, stateDir, project); m.Routes["app"] != 0 {
		t.Fatalf("route must be removed after a signal: %+v", m.Routes)
	}
}

func pgidMembers(t *testing.T, pgid int) []string {
	t.Helper()
	out, err := exec.Command("ps", "-eo", "pid=,pgid=,command=").Output()
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[1] == strconv.Itoa(pgid) {
			found = append(found, l)
		}
	}
	return found
}

// A member that ignores SIGTERM and outlives the leader must not be left behind.
func TestHostStopKillsGroupMembersThatOutliveTheLeader(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sh", "-c", `(trap "" TERM; exec sleep 600) & wait`)...); code != 0 {
		t.Fatalf("start: %d %s", code, stderr)
	}
	pid := readManifestAt(t, stateDir, project).HostProcs["app"].PID
	if len(pgidMembers(t, pid)) < 3 {
		t.Fatalf("expected supervisor, sh and sleep in the group: %v", pgidMembers(t, pid))
	}
	if _, stderr, code := runCLI(t, append(g, "host", "stop", "app")...); code != 0 {
		t.Fatalf("stop: %d %s", code, stderr)
	}
	for end := time.Now().Add(2 * time.Second); len(pgidMembers(t, pid)) > 0 && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
	}
	if left := pgidMembers(t, pid); len(left) > 0 {
		t.Fatalf("group members left: %v", left)
	}
}

// A stop flag from a stopper that died must not stop the supervisor cleaning up.
func TestSupervisorIgnoresStopFlagOfDeadStopper(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sh", "-c", "while :; do sleep 1; done")...); code != 0 {
		t.Fatalf("start: %d %s", code, stderr)
	}
	pid := readManifestAt(t, stateDir, project).HostProcs["app"].PID
	flag := filepath.Join(stateDir, project, "host-app.stopping")
	if err := os.WriteFile(flag, []byte("999999 jiffies:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	syscall.Kill(-pid, syscall.SIGTERM) // not through stop: the supervisor exits by itself
	if !waitDead(pid, 5*time.Second) {
		t.Fatal("supervisor did not exit")
	}
	m := readManifestAt(t, stateDir, project)
	if _, ok := m.HostProcs["app"]; ok || m.Routes["app"] != 0 {
		t.Fatalf("supervisor must clean up after a dead stopper: %+v %+v", m.HostProcs, m.Routes)
	}
	if _, err := os.Stat(flag); err == nil {
		t.Fatal("dead stopper's flag must be removed")
	}
}

func TestHostRefusesSecondRunAndOutsideDir(t *testing.T) {
	root, stateDir, _ := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sh", "-c", "while :; do sleep 1; done")...); code != 0 {
		t.Fatalf("start: %d %s", code, stderr)
	}
	if _, stderr, code := runCLI(t, append(g, "host", "app", "--", "true")...); code == 0 || !strings.Contains(stderr, "already running") {
		t.Fatalf("a second run of the same name must be refused: %d %s", code, stderr)
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "link"))
	for _, d := range []string{"..", "../x", "link"} {
		if _, stderr, code := runCLI(t, append(g, "host", "other", "--dir", d, "--", "true")...); code == 0 || !strings.Contains(stderr, "outside the checkout") && !strings.Contains(stderr, "--dir") {
			t.Errorf("--dir %s must be refused: %d %s", d, code, stderr)
		}
	}
}
