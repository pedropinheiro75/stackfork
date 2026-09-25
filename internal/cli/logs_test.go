package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogsPrintsServiceOutput covers task 4.1's "unit tests for the new
// subcommands with fake docker": `stackfork logs [SERVICE]` runs `docker
// compose ... logs --tail N [--follow] [SERVICE]`.
func TestLogsPrintsServiceOutput(t *testing.T) {
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
	os.Setenv("DOCKER_FAKE_LOGS", "db-1  | ready to accept connections\n")
	defer os.Unsetenv("DOCKER_FAKE_LOGS")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "logs", "db", "--tail", "50")
	if code != 0 {
		t.Fatalf("logs failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "ready to accept connections") {
		t.Fatalf("expected log output, got %q", out)
	}
	logData, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "logs --tail 50 db") {
		t.Fatalf("expected a `logs --tail 50 db` docker compose invocation, got:\n%s", logData)
	}
}

// TestLogsRedactsCanarySecret covers task 4.1a: a secret stackfork
// resolved for this instance never reaches the terminal, even when a
// container prints it verbatim.
func TestLogsRedactsCanarySecret(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	const canary = "canary-log-secret-4-1a"
	t.Setenv("SF_TEST_LOGS_CANARY", canary)

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "npm_token=env:SF_TEST_LOGS_CANARY", "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}

	os.Setenv("DOCKER_FAKE_LOGS", "app-1  | using token "+canary+" to authenticate\n")
	defer os.Unsetenv("DOCKER_FAKE_LOGS")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "logs")
	if code != 0 {
		t.Fatalf("logs failed: code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(out, canary) {
		t.Fatalf("logs leaked the secret value: %q", out)
	}
	if !strings.Contains(out, "[redacted:npm_token]") {
		t.Fatalf("expected a redaction marker, got %q", out)
	}
}
