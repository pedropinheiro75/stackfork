package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStartStopService covers fix 5: `stackfork start SERVICE` and
// `stackfork stop SERVICE` run `docker compose start/stop SERVICE` on the
// prepared snapshot, the same way `restart` does.
func TestStartStopService(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}

	for _, action := range []string{"start", "stop"} {
		os.Truncate(log, 0)
		out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, action, "db")
		if code != 0 {
			t.Fatalf("%s failed: code=%d stderr=%s out=%s", action, code, stderr, out)
		}
		logData, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(logData), action+" db") {
			t.Fatalf("expected a `%s db` docker compose invocation, got:\n%s", action, logData)
		}
	}
}

// TestStartStopUnknownServiceRefused covers the shared "specify a service
// in this snapshot" guard.
func TestStartStopUnknownServiceRefused(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}

	for _, action := range []string{"start", "stop"} {
		_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, action, "does-not-exist")
		if code == 0 {
			t.Fatalf("expected %s of an unknown service to fail", action)
		}
		if !strings.Contains(stderr, "Specify a service in this snapshot") {
			t.Fatalf("expected the snapshot-service error for %s, got %q", action, stderr)
		}
	}
}

// restart/start/stop take several services and pass them in one docker call.
func TestRestartSeveralServices(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services:\n  app:\n    image: alpine\n  db:\n    image: postgres:17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "restart", "app", "db"); code != 0 {
		t.Fatalf("restart: %s", stderr)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "restart app db") {
		t.Fatalf("one restart call for both services expected:\n%s", b)
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "restart", "app", "ghost"); code == 0 || !strings.Contains(stderr, "Specify a service") {
		t.Fatalf("an unknown service must refuse the whole call: %d %s", code, stderr)
	}
}
