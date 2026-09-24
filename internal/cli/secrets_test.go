package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 6.1: --secret NAME=env:VAR is stored by reference, resolved at up,
// and never appears in the manifest, the snapshot or a docker invocation.
func TestSecretEnvReferenceResolvedAtUp(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	const canary = "canary-env-secret-value-6-1"
	t.Setenv("SF_TEST_SECRET_TOKEN", canary)
	os.Unsetenv("npm_token")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "npm_token=env:SF_TEST_SECRET_TOKEN", "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, out)
	}
	stateDirOut := prepared["state"].(string)

	manifestData, err := os.ReadFile(filepath.Join(stateDirOut, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestData), `"npm_token": "env:SF_TEST_SECRET_TOKEN"`) {
		t.Fatalf("expected manifest to store the reference by name, got %s", manifestData)
	}
	if strings.Contains(string(manifestData), canary) {
		t.Fatal("manifest leaked the secret value")
	}

	configData, err := os.ReadFile(filepath.Join(stateDirOut, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"npm_token"`) {
		t.Fatalf("expected snapshot to declare the npm_token secret, got %s", configData)
	}
	if strings.Contains(string(configData), canary) {
		t.Fatal("snapshot leaked the secret value")
	}

	os.Truncate(log, 0)
	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	logData, _ := os.ReadFile(log)
	if strings.Contains(string(logData), canary) {
		t.Fatal("docker invocation log leaked the secret value")
	}
	if os.Getenv("npm_token") != canary {
		t.Fatalf("expected the resolved value in the process env passed to docker compose, got %q", os.Getenv("npm_token"))
	}
}

// Task 6.1: an unresolvable reference fails at up, naming the secret and
// the reference but never a partial value.
func TestSecretMissingVarFailsAtUp(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	os.Unsetenv("SF_TEST_MISSING_VAR")

	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "npm_token=env:SF_TEST_MISSING_VAR", "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "npm_token") || !strings.Contains(stderr, "SF_TEST_MISSING_VAR") {
		t.Fatalf("expected error naming the secret and reference, got %s", stderr)
	}
}

// Task 6.1: a malformed --secret flag is refused at prepare, before
// anything is written.
func TestSecretMalformedFlagRefusedAtPrepare(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "not-a-valid-flag", "prepare", "-f", composePath)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "not-a-valid-flag") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}
