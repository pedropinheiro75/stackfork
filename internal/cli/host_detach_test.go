//go:build !windows

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
			if p.PID > 1 {
				syscall.Kill(-p.PID, syscall.SIGKILL)
				syscall.Kill(p.PID, syscall.SIGKILL)
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
