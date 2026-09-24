package cli

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// preparedManifest runs `prepare` with the given extra args and returns the
// decoded manifest.json for the instance it produced.
func preparedManifest(t *testing.T, root, stateDir, legacyStateDir string, extraArgs ...string) *state.Manifest {
	t.Helper()
	args := []string{"--root", root, "--state-dir", stateDir}
	if legacyStateDir != "" {
		args = append(args, "--legacy-state-dir", legacyStateDir)
	}
	args = append(args, "prepare")
	args = append(args, extraArgs...)
	out, stderr, code := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("prepare failed: code=%d stderr=%s", code, stderr)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, out)
	}
	manifestPath := filepath.Join(prepared["state"].(string), "manifest.json")
	m, err := state.ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPreparePersistsStablePortsAndReusesOnReprepare(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	first := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath)
	if len(first.Ports) != 1 {
		t.Fatalf("expected one stable port allocation, got %v", first.Ports)
	}
	alloc := first.Ports[0]
	if alloc.Service != "db" || alloc.Target != 5432 || alloc.Host <= 0 {
		t.Fatalf("unexpected allocation: %+v", alloc)
	}

	configData, err := os.ReadFile(filepath.Join(stateDir, first.Project, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"published": "`+strconv.Itoa(alloc.Host)+`"`) {
		t.Fatalf("expected the snapshot to publish the allocated port %d, got:\n%s", alloc.Host, configData)
	}

	second := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath)
	if len(second.Ports) != 1 || second.Ports[0].Host != alloc.Host {
		t.Fatalf("expected re-prepare to keep the same port %d, got %v", alloc.Host, second.Ports)
	}
}

func TestPrepareReassignPortsPicksANewPortWhenRecordedOneIsBusy(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	first := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath)
	firstPort := first.Ports[0].Host

	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(firstPort))
	if err != nil {
		t.Skipf("could not occupy port %d to test reassignment: %v", firstPort, err)
	}
	defer l.Close()

	reassigned := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath, "--reassign-ports")
	if reassigned.Ports[0].Host == firstPort {
		t.Fatalf("expected --reassign-ports to pick a different port than the now-busy %d", firstPort)
	}
}

func TestPrepareRandomPortsSkipsStableAllocation(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	m := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath, "--random-ports")
	if len(m.Ports) != 0 {
		t.Fatalf("expected no stable port allocation under --random-ports, got %v", m.Ports)
	}
	configData, err := os.ReadFile(filepath.Join(stateDir, m.Project, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), `"published"`) {
		t.Fatalf("expected no published port under --random-ports, got:\n%s", configData)
	}
}

// TestPrepareLegacyInstanceKeepsRandomPortsUnlessReassigned covers design.md
// D2/D7: a legacy (idv-) instance must not silently gain stable ports on a
// plain re-prepare - only --reassign-ports opts it in, so a running legacy
// stack's bookmarks and OAuth redirect URIs never move under it.
func TestPrepareLegacyInstanceKeepsRandomPortsUnlessReassigned(t *testing.T) {
	installFakeDocker(t)
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

	plain := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath)
	if plain.Project != legacyProject {
		t.Fatalf("expected prepare to target the legacy instance %q, got %q", legacyProject, plain.Project)
	}
	if len(plain.Ports) != 0 {
		t.Fatalf("expected a plain re-prepare to leave a legacy instance without stable ports, got %v", plain.Ports)
	}

	reassigned := preparedManifest(t, root, stateDir, legacyStateDir, "-f", composePath, "--reassign-ports")
	if len(reassigned.Ports) != 1 {
		t.Fatalf("expected --reassign-ports to allocate stable ports for a legacy instance, got %v", reassigned.Ports)
	}
}

// TestPrepareTwoInstancesGetDistinctPorts covers design.md D7's "two
// instances of the same project are prepared" scenario end to end through
// the CLI: two worktrees (different roots, so different project hashes)
// sharing one state directory must never collide on a host port.
func TestPrepareTwoInstancesGetDistinctPorts(t *testing.T) {
	installFakeDocker(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	rootA := initRepo(t)
	rootB := initRepo(t)
	for _, r := range []string{rootA, rootB} {
		os.WriteFile(filepath.Join(r, "compose.yaml"), []byte(fixtureCompose), 0o600)
	}

	a := preparedManifest(t, rootA, stateDir, legacyStateDir, "-f", filepath.Join(rootA, "compose.yaml"))
	b := preparedManifest(t, rootB, stateDir, legacyStateDir, "-f", filepath.Join(rootB, "compose.yaml"))
	if a.Project == b.Project {
		t.Fatalf("expected two distinct projects, both got %q", a.Project)
	}
	if a.Ports[0].Host == b.Ports[0].Host {
		t.Fatalf("expected distinct stable ports for two instances, both got %d", a.Ports[0].Host)
	}
}

const scaledPublishedCompose = `services:
  web:
    image: nginx:alpine
    deploy:
      replicas: 2
    ports:
      - "8080:80"
`

// TestPrepareRefusesPublishedPortOnScaledService covers task 8.4: a service
// with more than one replica can't publish a host port, since Docker can't
// bind two containers to the same one.
func TestPrepareRefusesPublishedPortOnScaledService(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(scaledPublishedCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 1 {
		t.Fatalf("expected prepare to refuse, got code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "more than one replica") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}
