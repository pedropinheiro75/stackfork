package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readLog(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProxyUpFreshHasRestartPolicy(t *testing.T) {
	log := installFakeDocker(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-fresh")
	t.Setenv("DOCKER_FAKE_PROXY_MISSING", "1")
	stateDir := filepath.Join(t.TempDir(), "state")
	_, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", t.TempDir(), "proxy", "up", "--port", "18081")
	if code != 0 {
		t.Fatalf("proxy up failed: %s", stderr)
	}
	l := readLog(t, log)
	if !strings.Contains(l, "run -d --name sf-proxy-test-fresh --restart unless-stopped") {
		t.Fatalf("expected --restart unless-stopped on docker run:\n%s", l)
	}
}

func TestProxyUpStartsStoppedProxy(t *testing.T) {
	log := installFakeDocker(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-stopped")
	stateDir := filepath.Join(t.TempDir(), "state")
	legacy := t.TempDir()
	// First up records the port; then the container "exists" but is stopped.
	t.Setenv("DOCKER_FAKE_PROXY_MISSING", "1")
	if _, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacy, "proxy", "up", "--port", "18082"); code != 0 {
		t.Fatalf("proxy up failed: %s", stderr)
	}
	t.Setenv("DOCKER_FAKE_PROXY_MISSING", "")
	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacy, "proxy", "up", "--port", "18082"); code != 0 {
		t.Fatalf("second proxy up failed: %s", stderr)
	}
	l := readLog(t, log)
	if !strings.Contains(l, "start sf-proxy-test-stopped") {
		t.Fatalf("expected docker start:\n%s", l)
	}
	if strings.Contains(l, "rm -f") || strings.Contains(l, "run -d") {
		t.Fatalf("a stopped proxy must be started, not recreated:\n%s", l)
	}

	// A different port recreates it.
	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacy, "proxy", "up", "--port", "18083"); code != 0 {
		t.Fatalf("third proxy up failed: %s", stderr)
	}
	if l := readLog(t, log); !strings.Contains(l, "rm -f sf-proxy-test-stopped") || !strings.Contains(l, "run -d") {
		t.Fatalf("a different port must recreate the proxy:\n%s", l)
	}
}

func writeCaddyfile(t *testing.T, stateDir, body string) {
	t.Helper()
	dir := filepath.Join(stateDir, "proxy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Caddyfile"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const caddyWithRoute = "{\n\tauto_https off\n}\n\nhttp://a.localhost {\n\treverse_proxy host.docker.internal:1\n}\n"

func TestProxyDownWarnings(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-warn")
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureHTTPCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	legacy := t.TempDir()
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacy, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	base := []string{"--root", root, "--state-dir", stateDir, "--legacy-state-dir", legacy}

	// No Caddyfile: the proxy was never used, so no warning anywhere.
	for _, cmd := range []string{"status", "urls"} {
		if _, stderr, _ := runCLI(t, append(base, cmd)...); strings.Contains(stderr, "proxy up") {
			t.Fatalf("%s warned with no routes: %s", cmd, stderr)
		}
	}

	writeCaddyfile(t, stateDir, caddyWithRoute)
	for _, cmd := range []string{"status", "urls"} {
		if _, stderr, _ := runCLI(t, append(base, cmd)...); !strings.Contains(stderr, "run `stackfork proxy up`") {
			t.Fatalf("%s: expected the proxy-down warning, got %q", cmd, stderr)
		}
	}
	out, _, _ := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacy, "doctor")
	if !strings.Contains(out, "[warn] proxy running") {
		t.Fatalf("doctor: expected a proxy running warning:\n%s", out)
	}
	list, _, _ := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", legacy, "list", "--json")
	if !strings.Contains(list, `"hint"`) {
		t.Fatalf("inventory (list/dashboard) should carry the proxy hint:\n%s", list)
	}

	// Proxy running: no warning.
	t.Setenv("DOCKER_FAKE_PROXY_RUNNING", "true")
	if _, stderr, _ := runCLI(t, append(base, "status")...); strings.Contains(stderr, "proxy up") {
		t.Fatalf("warned while the proxy runs: %s", stderr)
	}
}
