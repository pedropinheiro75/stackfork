package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// TestListTableAndJSON covers task 1.4: `list` and `list --json` on top of
// the shared inventory, for a prepared-but-not-started instance (no docker
// daemon needed, same fake docker as the other CLI tests).
func TestListTableAndJSON(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	m := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath)

	out, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "list")
	if code != 0 {
		t.Fatalf("list failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, m.Project) || !strings.Contains(out, "stopped") {
		t.Fatalf("expected table to mention project and stopped state, got:\n%s", out)
	}

	jsonOut, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "list", "--json")
	if code != 0 {
		t.Fatalf("list --json failed: code=%d stderr=%s", code, stderr)
	}
	var inv struct {
		Version   string `json:"version"`
		Instances []struct {
			Project string `json:"project"`
			State   string `json:"state"`
		} `json:"instances"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &inv); err != nil {
		t.Fatalf("list --json output not JSON: %v\n%s", err, jsonOut)
	}
	if len(inv.Instances) != 1 || inv.Instances[0].Project != m.Project {
		t.Fatalf("expected one instance %q, got %+v", m.Project, inv.Instances)
	}
	if inv.Instances[0].State != "stopped" {
		t.Fatalf("expected stopped state, got %q", inv.Instances[0].State)
	}
}

// TestListNeverLeaksSecretValues is task 1.5's canary: a secret's resolved
// value must never appear in `list`, `list --json`, or the inventory
// struct's own JSON, since manifests only ever store a reference (design.md
// D6) and Collect never resolves one.
func TestListNeverLeaksSecretValues(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	const canary = "canary-leak-check-1-5"
	t.Setenv("SF_TEST_LIST_CANARY", canary)

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir,
		"--secret", "npm_token=env:SF_TEST_LIST_CANARY", "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}

	out, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "list")
	if code != 0 {
		t.Fatalf("list failed: %s", stderr)
	}
	if strings.Contains(out, canary) {
		t.Fatalf("list leaked the secret value:\n%s", out)
	}

	jsonOut, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "list", "--json")
	if code != 0 {
		t.Fatalf("list --json failed: %s", stderr)
	}
	if strings.Contains(jsonOut, canary) {
		t.Fatalf("list --json leaked the secret value:\n%s", jsonOut)
	}

	inv, err := inventory.Collect(inventory.Options{NewStateBase: stateDir, LegacyStateBase: legacyStateDir, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), canary) {
		t.Fatalf("Inventory JSON leaked the secret value:\n%s", b)
	}
}
