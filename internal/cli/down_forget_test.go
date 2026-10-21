package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forgetFixture prepares one instance with the fake docker and returns what
// the tests below need.
func forgetFixture(t *testing.T) (log, root, stateDir, project, instanceDir string) {
	t.Helper()
	log = installFakeDocker(t)
	root = initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir = filepath.Join(t.TempDir(), "state")
	m := preparedManifest(t, root, stateDir, "", "-f", composePath)
	return log, root, stateDir, m.Project, filepath.Join(stateDir, m.Project)
}

func fakeLog(log string) string { b, _ := os.ReadFile(log); return string(b) }

func TestDownForgetNeedsVolumes(t *testing.T) {
	log, root, stateDir, _, dir := forgetFixture(t)
	os.Truncate(log, 0)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--forget")
	if code == 0 || !strings.Contains(stderr, "--volumes") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	// Run flattens every error to exit 1; the command itself reports a usage error (2).
	var out strings.Builder
	if c, err := cmdUpDownRunExec(&out, &out, "down", []string{"--forget"}, root, "main", "p", dir, stateDir, ""); c != 2 || err == nil {
		t.Fatalf("code=%d err=%v", c, err)
	}
	if strings.Contains(fakeLog(log), "down") {
		t.Fatalf("docker down must not run:\n%s", fakeLog(log))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("state must stay: %v", err)
	}
}

func TestDownVolumesForgetRemovesInstance(t *testing.T) {
	log, root, stateDir, _, dir := forgetFixture(t)
	os.Truncate(log, 0)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--volumes", "--forget")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(fakeLog(log), "down --remove-orphans --volumes") {
		t.Fatalf("expected down --volumes:\n%s", fakeLog(log))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("instance state must be gone, stat err=%v", err)
	}
}

func TestDownVolumesWithoutForgetKeepsInstance(t *testing.T) {
	_, root, stateDir, _, dir := forgetFixture(t)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--volumes"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("state must stay: %v", err)
	}
}

func TestDownForgetKeepsInstanceWhenDownFails(t *testing.T) {
	_, root, stateDir, _, dir := forgetFixture(t)
	t.Setenv("DOCKER_FAKE_EXIT", "1")
	if _, _, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--volumes", "--forget"); code == 0 {
		t.Fatal("expected a failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("state must stay when down fails: %v", err)
	}
}
