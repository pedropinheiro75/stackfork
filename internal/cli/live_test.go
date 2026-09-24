package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func liveEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("STACKFORK_LIVE") != "1" {
		t.Skip("set STACKFORK_LIVE=1 to run live Docker tests")
	}
}

var (
	liveBinaryOnce sync.Once
	liveBinaryPath string
	liveBinaryErr  error
)

// stackforkBinary builds the real stackfork binary once per test run, so
// live tests exercise the actual CLI a user would run, not just cli.Run
// in-process.
func stackforkBinary(t *testing.T) string {
	t.Helper()
	liveBinaryOnce.Do(func() {
		root := moduleRoot(t)
		dir, err := os.MkdirTemp("", "stackfork-bin-")
		if err != nil {
			liveBinaryErr = err
			return
		}
		liveBinaryPath = filepath.Join(dir, "stackfork")
		cmd := exec.Command("go", "build", "-o", liveBinaryPath, "./cmd/stackfork")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			liveBinaryErr = fmt.Errorf("go build ./cmd/stackfork: %w: %s", err, out)
		}
	})
	if liveBinaryErr != nil {
		t.Fatal(liveBinaryErr)
	}
	return liveBinaryPath
}

// dockerInventory is the snapshot the orchestrator's hard rules require:
// never touch resources this test did not create, verified by comparing
// docker ps -a / docker volume ls before and after.
type dockerInventory struct {
	containers map[string]bool
	volumes    map[string]bool
}

func snapshotDocker(t *testing.T) dockerInventory {
	t.Helper()
	ps := mustRun(t, "docker", "ps", "-aq")
	vol := mustRun(t, "docker", "volume", "ls", "-q")
	return dockerInventory{containers: toSet(ps), volumes: toSet(vol)}
}

func toSet(output string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Fields(output) {
		set[line] = true
	}
	return set
}

func assertSameInventory(t *testing.T, before, after dockerInventory) {
	t.Helper()
	if len(before.containers) != len(after.containers) || !mapsEqual(before.containers, after.containers) {
		t.Fatalf("container inventory changed: before=%v after=%v", before.containers, after.containers)
	}
	if len(before.volumes) != len(after.volumes) || !mapsEqual(before.volumes, after.volumes) {
		t.Fatalf("volume inventory changed: before=%v after=%v", before.volumes, after.volumes)
	}
}

func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func mustRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// tryRun runs a command and returns its output/error without failing the
// test, for the one assertion that expects failure (the branch-switch
// refusal).
func tryRun(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type liveInstance struct {
	binary      string
	root        string
	state       string
	legacyState string
	service     string
	marker      string
}

// cli builds the argument list every live-test invocation shares. Both
// state dirs are always pointed at test-scratch paths (never the real
// default state directories, current or legacy): the port allocator added
// in task 8.1 reads every manifest under both dirs, and a live test must
// never touch the author's real, running instances even read-only.
func (li liveInstance) cli(args ...string) []string {
	base := []string{"--root", li.root, "--state-dir", li.state}
	if li.legacyState != "" {
		base = append(base, "--legacy-state-dir", li.legacyState)
	}
	return append(base, args...)
}

func (li liveInstance) run(t *testing.T, args ...string) string {
	t.Helper()
	return mustRun(t, li.binary, li.cli(args...)...)
}

func (li liveInstance) where(t *testing.T) map[string]interface{} {
	t.Helper()
	out := li.run(t, "where")
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("where output not JSON: %v\n%s", err, out)
	}
	return m
}

// TestLiveSixInstances ports the original Python CLI's live(): six real
// Postgres instances from the testdata/ fixtures (two checkouts per
// fixture), run concurrently, exercising unique ports/volumes, separate
// data, restart persistence, orphan removal on re-prepare, the
// branch-switch refusal, and scoped teardown.
func TestLiveSixInstances(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	modRoot := moduleRoot(t)
	testdata := filepath.Join(modRoot, "testdata")

	before := snapshotDocker(t)
	defer func() {
		after := snapshotDocker(t)
		assertSameInventory(t, before, after)
	}()

	type fixtureDef struct{ name, service, extra string }
	fixtures := []fixtureDef{
		{"pg17-fixed-name", "db", "orphan"},
		{"pg18-redis", "postgres", "/var/lib/postgresql"},
		{"pg14-missing-initdb", "postgres", "/var/lib/postgresql/data"},
	}

	scratch := t.TempDir()
	var instances []liveInstance
	stateDir := filepath.Join(scratch, "state")
	legacyStateDir := filepath.Join(scratch, "legacy-state")

	for _, f := range fixtures {
		for _, slot := range []string{"a", "b"} {
			root := filepath.Join(scratch, slot, f.name)
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			mustRun(t, "git", "init", "-q", root)
			data, err := os.ReadFile(filepath.Join(testdata, f.name, "compose.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "compose.yaml"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			var adapter string
			if f.extra == "orphan" {
				adapter = "services:\n  orphan:\n    image: nginx:alpine\n"
			} else {
				adapter = fmt.Sprintf("services:\n  %s:\n    volumes: !override\n      - fixture_data:%s\n    healthcheck:\n      test: [CMD-SHELL, \"pg_isready -U postgres\"]\n      interval: 2s\n      timeout: 3s\n      retries: 30\nvolumes:\n  fixture_data: {}\n", f.service, f.extra)
			}
			if err := os.WriteFile(filepath.Join(root, "adapter.yaml"), []byte(adapter), 0o600); err != nil {
				t.Fatal(err)
			}
			li := liveInstance{binary: bin, root: root, state: stateDir, legacyState: legacyStateDir, service: f.service, marker: f.name + "-" + slot}
			li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"), "-f", filepath.Join(root, "adapter.yaml"))
			instances = append(instances, li)
		}
	}

	defer func() {
		// Final scoped teardown, mirroring the Python test's finally block:
		// bring every instance's CURRENT project down directly via compose,
		// regardless of what state the test left it in.
		for _, li := range instances {
			identity := li.where(t)
			state, _ := identity["state"].(string)
			project, _ := identity["project"].(string)
			exec.Command("docker", "compose", "--env-file", os.DevNull, "-p", project,
				"-f", filepath.Join(state, "compose.json"), "--profile", "*",
				"down", "--remove-orphans", "--volumes").Run()
		}
	}()

	// t.Fatal is not safe to call from a non-test goroutine, so `up` runs
	// concurrently (mirroring the Python test's ThreadPoolExecutor) with
	// errors collected and reported back on the main test goroutine.
	errs := make([]error, len(instances))
	var wg sync.WaitGroup
	for i, li := range instances {
		wg.Add(1)
		go func(i int, li liveInstance) {
			defer wg.Done()
			cmd := exec.Command(li.binary, li.cli("up", "--service", li.service, "--wait-timeout", "90")...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs[i] = fmt.Errorf("%s: %v\n%s", li.marker, err, out)
			}
		}(i, li)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		t.Fatal("one or more instances failed to start")
	}

	published := map[string]bool{}
	volumes := map[string]bool{}
	for _, li := range instances {
		out := li.run(t, "status")
		var container map[string]interface{}
		var found bool
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			if err := json.Unmarshal([]byte(line), &container); err != nil {
				t.Fatal(err)
			}
			found = true
			break
		}
		if !found {
			t.Fatalf("%s: no status rows", li.marker)
		}
		publishers, _ := container["Publishers"].([]interface{})
		if len(publishers) == 0 {
			t.Fatalf("%s: database has no published host port", li.marker)
		}
		for _, raw := range publishers {
			p := raw.(map[string]interface{})
			if p["URL"] != "127.0.0.1" {
				t.Fatalf("%s: expected published URL 127.0.0.1, got %v", li.marker, p["URL"])
			}
			port := fmt.Sprintf("%v", p["PublishedPort"])
			if published[port] {
				t.Fatalf("%s: published port %s reused across instances", li.marker, port)
			}
			published[port] = true
		}
		id, _ := container["ID"].(string)
		inspectOut := mustRun(t, "docker", "inspect", id)
		var detail []map[string]interface{}
		if err := json.Unmarshal([]byte(inspectOut), &detail); err != nil {
			t.Fatal(err)
		}
		mounts, _ := detail[0]["Mounts"].([]interface{})
		var found1 bool
		for _, raw := range mounts {
			m := raw.(map[string]interface{})
			if m["Type"] == "volume" {
				name, _ := m["Name"].(string)
				if volumes[name] {
					t.Fatalf("%s: volume %s reused across instances", li.marker, name)
				}
				volumes[name] = true
				found1 = true
			}
		}
		if !found1 {
			t.Fatalf("%s: no volume mount found", li.marker)
		}

		sql := fmt.Sprintf("CREATE TABLE isolation_proof (value text); INSERT INTO isolation_proof VALUES ('%s');", li.marker)
		li.run(t, "exec", li.service, "psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", sql)
	}

	for _, li := range instances {
		got := strings.TrimSpace(li.run(t, "exec", li.service, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT value FROM isolation_proof"))
		if got != li.marker {
			t.Fatalf("%s: expected marker %q, got %q", li.marker, li.marker, got)
		}
	}
	t.Log("PASS: six live Postgres instances; unique ports/volumes, separate data")

	// Re-prepare removing a service (orphan) must tear it down via --remove-orphans.
	first := instances[0]
	first.run(t, "up", "--service", "orphan")
	if err := os.WriteFile(filepath.Join(first.root, "adapter.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first.run(t, "prepare", "-f", filepath.Join(first.root, "compose.yaml"), "-f", filepath.Join(first.root, "adapter.yaml"))
	first.run(t, "down")
	project, _ := first.where(t)["project"].(string)
	remaining := strings.TrimSpace(mustRun(t, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+project))
	if remaining != "" {
		t.Fatal("removed service was stranded after re-prepare and down")
	}
	t.Log("PASS: re-prepare and down remove services deleted from source configuration")

	// Ordinary stop retains data; deleting an instance's volume leaves its
	// sibling untouched.
	for i, li := range instances {
		if i%2 != 0 {
			continue
		}
		li.run(t, "down")
		li.run(t, "up", "--service", li.service)
		got := strings.TrimSpace(li.run(t, "exec", li.service, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT value FROM isolation_proof"))
		if got != li.marker {
			t.Fatalf("%s: data lost across restart, got %q", li.marker, got)
		}
		li.run(t, "down", "--volumes")
	}
	for i, li := range instances {
		if i%2 != 1 {
			continue
		}
		got := strings.TrimSpace(li.run(t, "exec", li.service, "psql", "-U", "postgres", "-d", "postgres", "-Atc", "SELECT value FROM isolation_proof"))
		if got != li.marker {
			t.Fatalf("%s: unaffected instance lost data, got %q", li.marker, got)
		}
	}

	// Branch-switch refusal, then explicit named cleanup of the old instance.
	last := instances[len(instances)-1]
	identity := last.where(t)
	oldProject, _ := identity["project"].(string)
	oldBranch, _ := identity["branch"].(string)
	mustRun(t, "git", "-C", last.root, "symbolic-ref", "HEAD", "refs/heads/other-branch")
	func() {
		defer mustRun(t, "git", "-C", last.root, "symbolic-ref", "HEAD", "refs/heads/"+oldBranch)
		last.run(t, "prepare", "-f", filepath.Join(last.root, "compose.yaml"), "-f", filepath.Join(last.root, "adapter.yaml"))
		out, err := tryRun(last.binary, last.cli("up", "--service", last.service)...)
		if err == nil {
			t.Fatal("expected `up` to refuse while another branch stack runs from this checkout")
		}
		if !strings.Contains(out, "Another branch stack") {
			t.Fatalf("expected the branch-switch refusal message, got: %s", out)
		}
		last.run(t, "down", "--instance", oldProject, "--volumes")
	}()
	t.Log("PASS: branch-switch refusal and explicit cleanup of old branch instance")
}
