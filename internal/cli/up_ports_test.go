package cli

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestUpRefusesWhenRecordedPortIsBusy covers task 8.3: `up` must fail,
// naming the service and port and suggesting --reassign-ports, rather than
// silently picking a different port, when a recorded stable port is held by
// something outside this project.
func TestUpRefusesWhenRecordedPortIsBusy(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	m := preparedManifest(t, root, stateDir, "", "-f", composePath)
	port := m.Ports[0].Host

	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Skipf("could not occupy port %d: %v", port, err)
	}
	defer l.Close()

	os.Truncate(log, 0)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "db") || !strings.Contains(stderr, strconv.Itoa(port)) || !strings.Contains(stderr, "--reassign-ports") {
		t.Fatalf("expected the refusal to name the service, port and --reassign-ports, got: %s", stderr)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "up -d") {
		t.Fatalf("expected `docker compose up` to never run, log:\n%s", data)
	}
}
