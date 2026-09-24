package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

const fixtureHTTPCompose = `services:
  web:
    image: nginx:alpine
    ports:
      - "8080:8080"
    labels:
      ` + identity.LabelHTTP + `: "8080"
`

// TestURLsPortlessWhenProxyRouting covers task 9.4's two branches with a
// fake docker: the primary HTTP service's line is the stable
// http://hostname:port form while the proxy isn't running, and switches to
// the portless form (task 9.4, design.md D7 layer 3) once it is - every
// other published line is untouched either way, since only the primary
// service's URL changes.
func TestURLsPortlessWhenProxyRouting(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-urls")
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureHTTPCompose), 0o600); err != nil {
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
	hostname, _ := prepared["hostname"].(string)

	t.Setenv("DOCKER_FAKE_STATUS", `{"ID":"c1","Name":"web-1","Service":"web","State":"running","Health":"","Publishers":[{"Protocol":"tcp","PublishedPort":54321,"TargetPort":8080,"URL":"127.0.0.1"}]}`+"\n")

	// Branch 1: proxy not running (DOCKER_FAKE_PROXY_RUNNING unset) -> stable
	// http://hostname:port form.
	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "urls")
	if code != 0 {
		t.Fatalf("urls failed: code=%d stderr=%s", code, stderr)
	}
	wantStable := "web: http://" + hostname + ":54321 (8080/tcp)"
	if !strings.Contains(out, wantStable) {
		t.Fatalf("expected stable form %q, got %q", wantStable, out)
	}

	// Branch 2: proxy running -> portless form (default proxy port 80, never
	// configured in this test, so no :port suffix either).
	t.Setenv("DOCKER_FAKE_PROXY_RUNNING", "true")
	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "urls")
	if code != 0 {
		t.Fatalf("urls failed: code=%d stderr=%s", code, stderr)
	}
	wantPortless := "web: http://" + hostname
	if !strings.Contains(out, wantPortless) {
		t.Fatalf("expected portless form %q, got %q", wantPortless, out)
	}
	if strings.Contains(out, wantStable) {
		t.Fatalf("stable form must have been replaced, got %q", out)
	}
}

// TestRouteAddRemoveList covers task 9.3's manifest storage: add, list,
// remove round trip for a host-run app registration.
func TestRouteAddRemoveList(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-route")
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: code=%d stderr=%s", code, stderr)
	}

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "route", "list")
	if code != 0 {
		t.Fatalf("route list failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "No routes registered.") {
		t.Fatalf("expected no routes yet, got %q", out)
	}

	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "route", "add", "web", "5173")
	if code != 0 {
		t.Fatalf("route add failed: code=%d stderr=%s", code, stderr)
	}

	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "route", "list")
	if code != 0 {
		t.Fatalf("route list failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "web: 5173") {
		t.Fatalf("expected registered route, got %q", out)
	}

	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "route", "remove", "web")
	if code != 0 {
		t.Fatalf("route remove failed: code=%d stderr=%s", code, stderr)
	}
	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "route", "list")
	if code != 0 {
		t.Fatalf("route list failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "No routes registered.") {
		t.Fatalf("expected route removed, got %q", out)
	}
}

// TestProxyStatusOffline covers `proxy status` plumbing with no daemon
// reachable for real: not running, default port.
func TestProxyStatusOffline(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-status")
	stateDir := t.TempDir()

	out, stderr, code := runCLI(t, "--state-dir", stateDir, "proxy", "status")
	if code != 0 {
		t.Fatalf("proxy status failed: code=%d stderr=%s", code, stderr)
	}
	var info map[string]interface{}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("proxy status output not JSON: %v\n%s", err, out)
	}
	if info["name"] != "sf-proxy-test-status" {
		t.Fatalf("expected overridden container name, got %v", info["name"])
	}
	if info["running"] != false {
		t.Fatalf("expected not running with no daemon, got %v", info["running"])
	}
	if info["port"] != float64(80) {
		t.Fatalf("expected default port 80, got %v", info["port"])
	}
}

// TestProxyUpWritesCaddyfileAndPort covers task 9.1's persistence
// requirement ("the port must be configurable and stored in
// <state>/proxy/") using a fake docker so `docker run` never touches a real
// daemon.
func TestProxyUpWritesCaddyfileAndPort(t *testing.T) {
	log := installFakeDocker(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-up")
	stateDir := t.TempDir()

	_, stderr, code := runCLI(t, "--state-dir", stateDir, "proxy", "up", "--port", "18080")
	if code != 0 {
		t.Fatalf("proxy up failed: code=%d stderr=%s", code, stderr)
	}

	portData, err := os.ReadFile(filepath.Join(stateDir, "proxy", "port"))
	if err != nil {
		t.Fatalf("expected port file: %v", err)
	}
	if strings.TrimSpace(string(portData)) != "18080" {
		t.Fatalf("expected persisted port 18080, got %q", portData)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "proxy", "Caddyfile")); err != nil {
		t.Fatalf("expected Caddyfile written: %v", err)
	}

	logData, _ := os.ReadFile(log)
	if !strings.Contains(string(logData), "127.0.0.1:18080:80") {
		t.Fatalf("expected docker run to publish 127.0.0.1:18080:80, got log:\n%s", logData)
	}
	if !strings.Contains(string(logData), "host.docker.internal:host-gateway") {
		t.Fatalf("expected --add-host host.docker.internal:host-gateway, got log:\n%s", logData)
	}
	if strings.Contains(string(logData), "-v /var/run/docker.sock") {
		t.Fatalf("proxy must never mount the Docker socket, got log:\n%s", logData)
	}
}
