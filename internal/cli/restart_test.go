package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestartService covers task 4.1's "unit tests for the new subcommands
// with fake docker": `stackfork restart SERVICE` runs `docker compose ...
// restart SERVICE` on the prepared snapshot.
func TestRestartService(t *testing.T) {
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

	os.Truncate(log, 0)
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "restart", "db")
	if code != 0 {
		t.Fatalf("restart failed: code=%d stderr=%s out=%s", code, stderr, out)
	}
	logData, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "restart db") {
		t.Fatalf("expected a `restart db` docker compose invocation, got:\n%s", logData)
	}
}

// TestRestartUnknownServiceRefused covers the "specify a service in this
// snapshot" guard, shared with run/exec.
func TestRestartUnknownServiceRefused(t *testing.T) {
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

	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "restart", "does-not-exist")
	if code == 0 {
		t.Fatalf("expected restart of an unknown service to fail")
	}
	if !strings.Contains(stderr, "Specify a service in this snapshot") {
		t.Fatalf("expected the snapshot-service error, got %q", stderr)
	}
}
