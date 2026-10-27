//go:build !windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// cascadeStack prepares a stack of db and worker (depends on db), records the
// plan's host apps api (db), app (api), studio (db) and other (nothing), and
// gives each a real running process. cascade turns stop_cascade on.
func cascadeStack(t *testing.T, cascade bool) (g []string, stateDir string, procs map[string]state.HostProc) {
	t.Helper()
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	body := "services:\n  db:\n    image: postgres:17\n  worker:\n    image: alpine\n    depends_on: [db]\n  cache:\n    image: redis\n"
	if err := os.WriteFile(composePath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir = filepath.Join(t.TempDir(), "state")
	g = []string{"--root", root, "--state-dir", stateDir}
	rec := func(args ...string) {
		t.Helper()
		if _, stderr, code := runCLI(t, append(append([]string(nil), g...), append([]string{"learn", "record"}, args...)...)...); code != 0 {
			t.Fatalf("learn record %v: %s", args, stderr)
		}
	}
	for name, deps := range map[string]string{"api": "db", "app": "api", "studio": "db", "other": ""} {
		args := []string{"step", "--id", name, "--kind", "host", "--name", name, "--cmd", "sleep 300"}
		if deps != "" {
			args = append(args, "--depends-on", deps)
		}
		rec(args...)
	}
	if cascade {
		rec("stop-cascade", "on")
	}
	if _, stderr, code := runCLI(t, append(append([]string(nil), g...), "prepare", "-f", composePath)...); code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	ms, _ := filepath.Glob(filepath.Join(stateDir, "*", "manifest.json"))
	if len(ms) != 1 {
		t.Fatalf("manifests %v", ms)
	}
	m, err := state.ReadManifest(ms[0])
	if err != nil {
		t.Fatal(err)
	}
	m.HostProcs = map[string]state.HostProc{}
	for _, n := range []string{"api", "app", "studio", "other"} {
		c := exec.Command("sleep", "300")
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		go c.Wait() //nolint:errcheck // reaped so a stopped host does not read as alive
		t.Cleanup(func() { syscall.Kill(-c.Process.Pid, syscall.SIGKILL) })
		started, err := state.ProcStart(c.Process.Pid)
		if err != nil {
			t.Skip("no process start time on this platform")
		}
		m.HostProcs[n] = state.HostProc{PID: c.Process.Pid, StartedAt: started, Cmdline: []string{"sleep", "300"}}
	}
	if err := state.WritePrivate(ms[0], m); err != nil {
		t.Fatal(err)
	}
	return g, stateDir, m.HostProcs
}

func up(p state.HostProc) bool { return state.HostIdentity(p) }

// Off (the default): a stop touches only what it names.
func TestStopWithoutCascadeIsUnchanged(t *testing.T) {
	log := installFakeDocker(t)
	g, _, procs := cascadeStack(t, false)
	os.Truncate(log, 0)
	out, stderr, code := runCLI(t, append(g, "stop", "db")...)
	if code != 0 {
		t.Fatalf("stop: %s", stderr)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), " stop db \n") || strings.Contains(string(b), "worker") {
		t.Fatalf("expected a plain `stop db`:\n%s", b)
	}
	if strings.Contains(out, "dependents") {
		t.Fatalf("no cascade line expected: %q", out)
	}
	for n, p := range procs {
		if !up(p) {
			t.Fatalf("%s must keep running", n)
		}
	}
}

// On: the host apps that depend on db, directly or not, stop before the
// compose stop, which carries the dependent service too.
func TestStopCascadesToDependentsFirst(t *testing.T) {
	log := installFakeDocker(t)
	g, stateDir, procs := cascadeStack(t, true)

	// a docker wrapper that notes which host apps still ran when `stop` came
	orig, _ := exec.LookPath("docker")
	wrapDir := t.TempDir()
	seen := filepath.Join(wrapDir, "alive-at-stop")
	var pids []string
	for _, n := range []string{"api", "app", "studio"} {
		pids = append(pids, fmt.Sprint(procs[n].PID))
	}
	script := "#!/bin/sh\ncase \" $* \" in *\" stop \"*) for p in " + strings.Join(pids, " ") + "; do kill -0 $p 2>/dev/null && echo $p >> " + seen + "; done;; esac\nexec " + orig + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapDir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Truncate(log, 0)

	out, stderr, code := runCLI(t, append(g, "stop", "db")...)
	if code != 0 {
		t.Fatalf("stop: %s", stderr)
	}
	if !strings.Contains(out, "stopped db and its dependents: api, app, studio, worker") {
		t.Fatalf("output %q", out)
	}
	if b, _ := os.ReadFile(seen); len(b) != 0 {
		t.Fatalf("host apps still ran when compose stop started: %s", b)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), " stop db worker \n") {
		t.Fatalf("one compose stop for db and its dependent service:\n%s", b)
	}
	if !up(procs["other"]) {
		t.Fatal("an unrelated host app must keep running")
	}
	ms, _ := filepath.Glob(filepath.Join(stateDir, "*", "manifest.json"))
	m, _ := state.ReadManifest(ms[0])
	if len(m.HostProcs) != 4 {
		t.Fatalf("rows stay so Start can respawn them: %v", m.HostProcs)
	}
	if r, _ := state.ReadHostRecord(filepath.Dir(ms[0]), "api"); r != nil && r.StoppedByPerson {
		t.Fatal("a cascaded host app is not stopped by a person: Start must bring it back")
	}

	// nothing depends on cache: unchanged
	os.Truncate(log, 0)
	out, _, code = runCLI(t, append(g, "stop", "cache")...)
	if b, _ := os.ReadFile(log); code != 0 || strings.Contains(out, "dependents") || !strings.Contains(string(b), " stop cache \n") {
		t.Fatalf("%d %q\n%s", code, out, b)
	}
}

// Stopping a host app stops the host apps that depend on it.
func TestHostStopCascades(t *testing.T) {
	installFakeDocker(t)
	g, _, procs := cascadeStack(t, true)
	out, stderr, code := runCLI(t, append(g, "host", "stop", "api")...)
	if code != 0 {
		t.Fatalf("host stop: %s", stderr)
	}
	if !strings.Contains(out, "stopped api and its dependents: app") {
		t.Fatalf("output %q", out)
	}
	if up(procs["api"]) || up(procs["app"]) || !up(procs["studio"]) || !up(procs["other"]) {
		t.Fatal("api and app stop; studio and other do not depend on api")
	}
}
