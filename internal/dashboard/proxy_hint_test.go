package dashboard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

// Routes exist (a Caddyfile with a site) but the proxy is down: the page and
// /api/instances both carry the "run `stackfork proxy up`" hint.
func TestProxyDownHint(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	if err := os.MkdirAll(filepath.Join(newBase, "proxy"), 0o700); err != nil {
		t.Fatal(err)
	}
	caddy := "http://a.localhost {\n\treverse_proxy host.docker.internal:1\n}\n"
	if err := os.WriteFile(filepath.Join(newBase, "proxy", "Caddyfile"), []byte(caddy), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := dashboard.New(newBase, t.TempDir(), "test", 55124)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55124"
	for _, path := range []string{"/", "/api/instances"} {
		body := doRequest(srv, "GET", path, host, nil).Body.String()
		if !strings.Contains(body, "stackfork proxy up") {
			t.Fatalf("GET %s: missing the proxy hint:\n%s", path, body)
		}
	}
}
