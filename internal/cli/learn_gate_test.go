package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gateRepo is a git checkout with a Compose file and nothing else: a
// repository that was never learned.
func gateRepo(t *testing.T) (root, compose, stateDir string) {
	t.Helper()
	root = initBareRepo(t)
	compose = filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(compose, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, compose, filepath.Join(t.TempDir(), "state")
}

// recordDraft is what most fixture tests need: `learn` ran, so the
// repository is no longer unknown.
func recordDraft(t *testing.T, root, stateDir string) {
	t.Helper()
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "learn"); code != 0 {
		t.Fatalf("learn failed: %s", stderr)
	}
}

func TestPrepareRefusesNeverLearnedRepo(t *testing.T) {
	log := installFakeDocker(t)
	root, compose, stateDir := gateRepo(t)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", compose)
	if code == 0 || !strings.Contains(stderr, "has not been learned yet: run `stackfork learn` first (a person can override with --force)") {
		t.Fatalf("expected the learn refusal, code=%d stderr=%s", code, stderr)
	}
	if data, _ := os.ReadFile(log); strings.Contains(string(data), "config") {
		t.Fatalf("no compose call expected, got:\n%s", data)
	}
}

func TestPrepareForceOverridesLearnGateAndWarns(t *testing.T) {
	installFakeDocker(t)
	root, compose, stateDir := gateRepo(t)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "--force", "-f", compose)
	if code != 0 || !strings.Contains(stderr, "has not been learned") || !strings.Contains(stderr, "--force") {
		t.Fatalf("expected success with a warning, code=%d stderr=%s", code, stderr)
	}
}

func TestUpRefusesNeverLearnedRepoWithoutInstance(t *testing.T) {
	installFakeDocker(t)
	root, _, stateDir := gateRepo(t)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code == 0 || !strings.Contains(stderr, "has not been learned yet") {
		t.Fatalf("expected the learn refusal, code=%d stderr=%s", code, stderr)
	}
}

func TestUpOnExistingInstanceOfNeverLearnedRepoStillWorks(t *testing.T) {
	installFakeDocker(t)
	root, compose, stateDir := gateRepo(t)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "--force", "-f", compose); code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10"); code != 0 {
		t.Fatalf("up on a prepared instance must work: code=%d stderr=%s", code, stderr)
	}
}

func TestPrepareAllowedForDraftKnowledge(t *testing.T) {
	installFakeDocker(t)
	root, compose, stateDir := gateRepo(t)
	recordDraft(t, root, stateDir)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", compose); code != 0 {
		t.Fatalf("a draft must be allowed: code=%d stderr=%s", code, stderr)
	}
}
