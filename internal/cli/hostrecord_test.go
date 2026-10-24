package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
