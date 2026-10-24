package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestHostWritesRecordOnStartAndExit(t *testing.T) {
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	t.Setenv("HOSTREC_TOKEN", "tok-0123456789abcdef")
	mp := filepath.Join(stateDir, project, "manifest.json")
	m := readManifestAt(t, stateDir, project)
	m.Secrets = map[string]string{"api_token": "env:HOSTREC_TOKEN"}
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	during := filepath.Join(t.TempDir(), "record-during.json")
	script := "cat " + filepath.Join(stateDir, project, "host-app.json") + " > " + during + "; echo tok-0123456789abcdef; exit 3"
	_, _, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", "sh", "-c", script)
	if code != 3 {
		t.Fatalf("code %d", code)
	}
	if b, _ := os.ReadFile(during); !strings.Contains(string(b), `"pid"`) || strings.Contains(string(b), "exited_at") {
		t.Errorf("record while running: %s", b)
	}
	dir := filepath.Join(stateDir, project)
	rec, err := state.ReadHostRecord(dir, "app")
	if err != nil || rec == nil {
		t.Fatalf("record: %v", err)
	}
	if rec.ExitCode == nil || *rec.ExitCode != 3 || rec.ExitedAt == "" || rec.PID == 0 || rec.SupervisorPID != os.Getpid() || rec.StartedAt == "" || rec.Port == 0 || rec.Cwd == "" {
		t.Errorf("record: %+v", rec)
	}
	if got := strings.Join(rec.Argv, " "); strings.Contains(got, "tok-0123456789abcdef") || !strings.HasPrefix(got, "sh -c") {
		t.Errorf("argv: %q", got)
	}
	// the secret in argv: run again with it as an argument
	runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", "sh", "-c", "exit 0", "tok-0123456789abcdef")
	rec, _ = state.ReadHostRecord(dir, "app")
	if strings.Contains(strings.Join(rec.Argv, " "), "tok-0123456789abcdef") || !strings.Contains(strings.Join(rec.Argv, " "), "[redacted:api_token]") {
		t.Errorf("argv not redacted: %q", rec.Argv)
	}
	if fi, _ := os.Stat(state.HostRecordPath(dir, "app")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
}

func TestHostLsListsStateAndExit(t *testing.T) {
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	g := []string{"--root", root, "--state-dir", stateDir}
	if out, _, code := runCLI(t, append(g, "host", "ls")...); code != 0 || !strings.Contains(out, "no host apps") {
		t.Fatalf("empty: %d %q", code, out)
	}
	runCLI(t, append(g, "host", "app", "--", "sh", "-c", "exit 3")...)
	// runCLI runs in this process, which outlives the command; a real stackfork has exited by now
	mp := filepath.Join(stateDir, project, "manifest.json")
	m := readManifestAt(t, stateDir, project)
	p := m.HostProcs["app"]
	p.PID, p.StartedAt = 999999, "1970-01-01T00:00:00Z"
	m.HostProcs["app"] = p
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runCLI(t, append(g, "host", "ls")...)
	if code != 0 {
		t.Fatalf("ls: %d %s", code, stderr)
	}
	for _, w := range []string{"NAME", "STATE", "app", "exited", "exited 3 at "} {
		if !strings.Contains(out, w) {
			t.Errorf("ls lacks %q:\n%s", w, out)
		}
	}
}

// `up` of an instance whose host app is down starts it again, after the
// instance lock is released (the supervisor takes that lock).
func TestUpStartsHostAppThatIsDown(t *testing.T) {
	root, stateDir, project, _ := envSetup(t, hostFixture)
	dir := filepath.Join(stateDir, project)
	m := readManifestAt(t, stateDir, project)
	m.HostProcs = map[string]state.HostProc{"api": {PID: 999999, StartedAt: "1970-01-01T00:00:00Z", Port: 40001,
		Cmdline: []string{"stackfork", "host", "--supervise", "api", "--", "bun", "dev"}}}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	var restarted []string
	lockFree := false
	old := lifecycle.HostRestart
	lifecycle.HostRestart = func(_ io.Writer, _, instanceDir, name, _, _ string) error {
		restarted = append(restarted, name)
		if l, err := state.TryLock(filepath.Join(instanceDir, ".lock")); err == nil {
			lockFree = true
			l.Release()
		}
		return nil
	}
	t.Cleanup(func() { lifecycle.HostRestart = old })
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "5")
	if code != 0 {
		t.Fatalf("up: %d %s", code, stderr)
	}
	if len(restarted) != 1 || restarted[0] != "api" || !lockFree {
		t.Fatalf("restarted %v, lock free at that point: %v", restarted, lockFree)
	}
	r, err := runs.Get(dir, 2) // envSetup's up was run 1
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, s := range r.Steps {
		ok = ok || (s.ID == "host-api" && s.Kind == "host" && s.State == runs.Succeeded)
	}
	if !ok {
		t.Errorf("run steps %+v", r.Steps)
	}
}
