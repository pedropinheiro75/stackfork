package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerNameOverride(t *testing.T) {
	if got := ContainerName(); got != "stackfork-proxy" {
		t.Fatalf("expected default stackfork-proxy, got %q", got)
	}
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test")
	if got := ContainerName(); got != "sf-proxy-test" {
		t.Fatalf("expected override, got %q", got)
	}
}

func TestReadWritePortDefault(t *testing.T) {
	state := t.TempDir()
	port, err := ReadPort(state)
	if err != nil {
		t.Fatal(err)
	}
	if port != DefaultPort {
		t.Fatalf("expected default port %d with nothing persisted, got %d", DefaultPort, port)
	}
	if err := WritePort(state, 18080); err != nil {
		t.Fatal(err)
	}
	port, err = ReadPort(state)
	if err != nil {
		t.Fatal(err)
	}
	if port != 18080 {
		t.Fatalf("expected persisted port 18080, got %d", port)
	}
	fi, err := os.Stat(filepath.Join(state, "proxy", "port"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 port file, got %o", fi.Mode().Perm())
	}
}

// GenerateCaddyfile must never try to get a TLS cert for a .localhost name
// (design.md D7 layer 3): auto_https off in the global block, http://
// (never https://) site addresses.
func TestGenerateCaddyfileNoAutoHTTPS(t *testing.T) {
	out := GenerateCaddyfile([]Route{
		{Hostname: "b.localhost", Target: "host.docker.internal:20002"},
		{Hostname: "a.localhost", Target: "host.docker.internal:20001"},
	})
	if !strings.Contains(out, "auto_https off") {
		t.Fatalf("expected auto_https off in global block:\n%s", out)
	}
	if strings.Contains(out, "https://") {
		t.Fatalf("expected no https:// site address:\n%s", out)
	}
	aIdx := strings.Index(out, "http://a.localhost")
	bIdx := strings.Index(out, "http://b.localhost")
	if aIdx < 0 || bIdx < 0 || aIdx > bIdx {
		t.Fatalf("expected routes sorted by hostname:\n%s", out)
	}
	if !strings.Contains(out, "reverse_proxy host.docker.internal:20001") {
		t.Fatalf("expected a's target in output:\n%s", out)
	}
}

func TestGenerateCaddyfileEmpty(t *testing.T) {
	out := GenerateCaddyfile(nil)
	if !strings.Contains(out, "auto_https off") {
		t.Fatalf("expected global block even with no routes:\n%s", out)
	}
	if strings.Contains(out, "reverse_proxy") {
		t.Fatalf("expected no site blocks:\n%s", out)
	}
}

func TestWriteCaddyfilePrivate(t *testing.T) {
	state := t.TempDir()
	if err := WriteCaddyfile(state, []Route{{Hostname: "a.localhost", Target: "host.docker.internal:20001"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(CaddyfilePath(state))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 Caddyfile, got %o", fi.Mode().Perm())
	}
}
