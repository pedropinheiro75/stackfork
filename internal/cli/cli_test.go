package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
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

// Ported behaviour from scripts/test_compose.py: prepare/up/status/urls/down
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
	t.Setenv("DOCKER_FAKE_PS", "idv-some-other-project\n")
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
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
