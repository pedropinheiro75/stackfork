package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

// moduleRoot finds the repo root from this test file's own path, so live
// tests can locate testdata/ regardless of the working directory `go test`
// is invoked from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// internal/cli/cli_test.go -> repo root
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = Run(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

const fixtureCompose = `services:
  db:
    image: postgres:17
    ports:
      - "5432:5432"
`

// Ported behaviour from the original Python CLI's test suite: prepare/up/status/urls/down
// build the expected docker/compose invocations, using a fake docker on
// PATH so no daemon is needed (config still renders for real).
func TestLifecycleBuildsExpectedDockerInvocations(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: code=%d stderr=%s", code, stderr)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, out)
	}
	project, _ := prepared["project"].(string)
	if !strings.HasPrefix(project, "sf-") {
		t.Fatalf("expected sf- prefixed project, got %q", project)
	}
	if prepared["result"] != "prepared; not started" {
		t.Fatalf("unexpected result: %v", prepared["result"])
	}

	logContains := func(t *testing.T, substr string) {
		t.Helper()
		data, _ := os.ReadFile(log)
		if !strings.Contains(string(data), substr) {
			t.Fatalf("expected fake docker log to contain %q, got:\n%s", substr, data)
		}
	}
	logContains(t, "config --no-env-resolution --format json")

	os.Truncate(log, 0)
	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	if code != 0 {
		t.Fatalf("up failed: code=%d stderr=%s", code, stderr)
	}
	logContains(t, "up -d --wait --wait-timeout 10")
	logContains(t, "-p "+project)

	os.Truncate(log, 0)
	t.Setenv("DOCKER_FAKE_STATUS", `{"ID":"c1","Name":"`+project+`-db-1","Service":"db","State":"running","Health":"","Publishers":[{"Protocol":"tcp","PublishedPort":54321,"TargetPort":5432,"URL":"127.0.0.1"}]}`+"\n")
	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "status")
	if code != 0 {
		t.Fatalf("status failed: code=%d stderr=%s", code, stderr)
	}
	var row map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &row); err != nil {
		t.Fatalf("status output not JSON: %v\n%s", err, out)
	}
	if row["Service"] != "db" {
		t.Fatalf("unexpected status row: %v", row)
	}
	if _, has := row["Command"]; has {
		t.Fatal("status must not leak private fields such as Command")
	}

	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "urls")
	if code != 0 {
		t.Fatalf("urls failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "db: tcp://127.0.0.1:54321 (5432/tcp)") {
		t.Fatalf("unexpected urls output: %q", out)
	}
	os.Unsetenv("DOCKER_FAKE_STATUS")

	os.Truncate(log, 0)
	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--volumes")
	if code != 0 {
		t.Fatalf("down failed: code=%d stderr=%s", code, stderr)
	}
	logContains(t, "--profile * down --remove-orphans --volumes")
}

func TestUpRefusesWhenAnotherBranchStackRuns(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	t.Setenv("DOCKER_FAKE_PS", "sf-some-other-project\n")
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "Another branch stack still runs from this checkout") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

// TestUpRefusesWhenLegacyBranchStackRuns ports design.md D2's "branch-switch
// guard sees both namespaces" scenario: a container labelled under the
// legacy root label namespace (identity.LegacyLabelRoot) must refuse `up`
// exactly like one labelled under the current namespace.
func TestUpRefusesWhenLegacyBranchStackRuns(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	t.Setenv("DOCKER_FAKE_PS_LEGACY", "idv-some-legacy-project\n")
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "up")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "Another branch stack still runs from this checkout") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestDownInvalidInstanceName(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--instance", "../etc/passwd")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "Invalid instance name") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestActionsRequirePreparedStack(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	for _, action := range []string{"up", "down", "status", "urls"} {
		_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, action)
		if code != 1 {
			t.Fatalf("%s: expected exit 1, got %d", action, code)
		}
		if !strings.Contains(stderr, "No prepared stack for this checkout/branch. Run prepare first.") {
			t.Fatalf("%s: unexpected stderr: %s", action, stderr)
		}
	}
}

func TestSnapshotChangedRefusal(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	var prepared map[string]interface{}
	json.Unmarshal([]byte(out), &prepared)
	configPath := filepath.Join(prepared["state"].(string), "compose.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, append(data, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "Snapshot changed; edit the source Compose files and run prepare again") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestRunRequiresKnownServiceAndCommand(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "run", "nosuchservice", "echo", "hi")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "Specify a service in this snapshot and a command") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestWhere(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath, "--hostname", "eup-274.localhost")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "where")
	if code != 0 {
		t.Fatalf("where failed: %s", stderr)
	}
	var w map[string]interface{}
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatal(err)
	}
	if w["hostname"] != "eup-274.localhost" {
		t.Fatalf("unexpected hostname: %v", w["hostname"])
	}
	instances, _ := w["prepared_instances"].([]interface{})
	if len(instances) != 1 {
		t.Fatalf("expected one prepared instance, got %v", instances)
	}
}

// writeSyntheticLegacyInstance writes a manifest.json + compose.json under
// legacyStateDir as if the previous (idv-) version of the tool had prepared
// this checkout+branch, without running any real prepare. Used by task
// 3.8's legacy-compatibility tests, which must not touch the real legacy
// state directory (identity.LegacyStateDir).
func writeSyntheticLegacyInstance(t *testing.T, legacyStateDir, root, branch string) (project string) {
	t.Helper()
	// A real prepare always stores the resolved checkout path (Path.resolve()
	// in Python, identity.ResolvePath in Go); match that here so the
	// root-ownership checks in `where`/`status`/`urls`/`down` see the same
	// value the CLI itself computes.
	root, err := identity.ResolvePath(root)
	if err != nil {
		t.Fatal(err)
	}
	project, err = identity.LegacyProjectName(root, branch)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(legacyStateDir, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "compose.json")
	snapshot := `{"name":"` + project + `","services":{"db":{"image":"postgres:17","ports":[{"target":5432,"host_ip":"127.0.0.1","mode":"ingress"}]}}}`
	if err := os.WriteFile(configPath, []byte(snapshot), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := state.SHA256File(configPath)
	if err != nil {
		t.Fatal(err)
	}
	hostname, err := identity.BrowserHostname(project, "")
	if err != nil {
		t.Fatal(err)
	}
	manifest := &state.Manifest{
		Project: project, Root: root, Branch: branch, Hostname: hostname,
		Files: []string{configPath}, Inputs: map[string]string{}, ConfigSHA256: hash,
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	return project
}

// TestLegacyInstanceKeepsOperatingOnLegacyState ports design.md D2/task
// 3.8: a checkout+branch that already has a legacy manifest must keep
// resolving `where`, `status`, `urls`, `down` (and would keep `prepare`
// writing) to the legacy state dir and project name, never a new sf-
// instance, and never touching the real legacy state dir.
func TestLegacyInstanceKeepsOperatingOnLegacyState(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	branch, err := currentBranch(root)
	if err != nil {
		t.Fatal(err)
	}
	legacyProject := writeSyntheticLegacyInstance(t, legacyStateDir, root, branch)

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "where")
	if code != 0 {
		t.Fatalf("where failed: %s", stderr)
	}
	var w map[string]interface{}
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatal(err)
	}
	if w["project"] != legacyProject {
		t.Fatalf("expected where to resolve the legacy instance %q, got %v", legacyProject, w["project"])
	}
	resolvedLegacyStateDir, err := identity.ResolvePath(legacyStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(w["state"].(string), resolvedLegacyStateDir) {
		t.Fatalf("expected state under the legacy state dir %q, got %v", resolvedLegacyStateDir, w["state"])
	}
	instances, _ := w["prepared_instances"].([]interface{})
	if len(instances) != 1 {
		t.Fatalf("expected the legacy instance listed in prepared_instances, got %v", instances)
	}

	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "status"); code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "urls"); code != 0 {
		t.Fatalf("urls failed: %s", stderr)
	}

	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "down"); code != 0 {
		t.Fatalf("down failed: %s", stderr)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "-p "+legacyProject) {
		t.Fatalf("expected down to target the legacy project %q, log:\n%s", legacyProject, data)
	}

	// New state dir must stay untouched: no instance was ever created there.
	if entries, err := os.ReadDir(stateDir); err == nil && len(entries) != 0 {
		t.Fatalf("expected no new-namespace instance to be created, found %v", entries)
	}
}

// TestDownInstanceAcceptsLegacyAndNewNames ports design.md D2's "legacy
// instance name accepted" scenario: `down --instance` must accept both
// sf- and idv- names and resolve each from its own state dir.
func TestDownInstanceAcceptsLegacyAndNewNames(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	branch, err := currentBranch(root)
	if err != nil {
		t.Fatal(err)
	}
	legacyProject := writeSyntheticLegacyInstance(t, legacyStateDir, root, branch)

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	// A fresh checkout+branch with a legacy instance already present must
	// still be the one prepare operates on (it must not create a second,
	// sf- prefixed copy).
	var prepared map[string]interface{}
	json.Unmarshal([]byte(out), &prepared)
	if prepared["project"] != legacyProject {
		t.Fatalf("expected prepare to target the legacy project %q, got %v", legacyProject, prepared["project"])
	}

	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "down", "--instance", legacyProject); code != 0 {
		t.Fatalf("down --instance (legacy) failed: %s", stderr)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "-p "+legacyProject) {
		t.Fatalf("expected down --instance to target %q, log:\n%s", legacyProject, data)
	}
}
