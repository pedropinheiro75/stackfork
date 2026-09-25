package dashboard_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestConnectionURLRenderedAsCopyButton covers task 3.7's page wiring: a
// Postgres service with a resolved port shows a "connection URL" copy
// button carrying the placeholder-safe string, and the canary password
// itself never reaches the page.
func TestConnectionURLRenderedAsCopyButton(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")

	const canary = "canary-connection-url-password"
	project := "sf-connurl-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{
		"services": map[string]interface{}{
			"db": map[string]interface{}{
				"image": "postgres:17",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("5432"), "published": "20030", "protocol": "tcp"},
				},
				"environment": map[string]interface{}{
					"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb", "POSTGRES_PASSWORD": canary,
				},
			},
		},
	}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}

	srv, err := dashboard.New(newBase, legacyBase, "test", 55701)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55701"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if strings.Contains(body, canary) {
		t.Fatalf("page leaked the password value")
	}
	if !strings.Contains(body, `class="endpoint-copy copyable connection-url"`) {
		t.Fatalf("expected a connection-url copy button:\n%s", body)
	}
	if !strings.Contains(body, "postgres://appuser:&lt;password&gt;@127.0.0.1:20030/appdb") &&
		!strings.Contains(body, "postgres://appuser:<password>@127.0.0.1:20030/appdb") {
		t.Fatalf("expected the placeholder connection URL in the button's data-copy/title:\n%s", body)
	}
}
