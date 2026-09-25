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

// TestUseCommandsAndEnvBlockRendered covers task 3.10's page wiring: a
// Postgres service's use-it commands render as copy pills and the stack's
// "Copy .env for host app" block renders inside the stack menu, both
// placeholder-only (no password value reaches the page).
func TestUseCommandsAndEnvBlockRendered(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")

	const canary = "canary-usecommands-render-password"
	project := "sf-usecmdrender-1"
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
					map[string]interface{}{"target": json.Number("5432"), "published": "20090", "protocol": "tcp"},
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

	srv, err := dashboard.New(newBase, legacyBase, "test", 55960)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55960"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if strings.Contains(body, canary) {
		t.Fatalf("page leaked the password value")
	}
	if !strings.Contains(body, `class="endpoint-copy copyable use-command"`) {
		t.Fatalf("expected use-command pills:\n%s", body)
	}
	if !strings.Contains(body, `psql &#34;postgres://appuser:&lt;password&gt;@127.0.0.1:20090/appdb&#34;`) {
		t.Fatalf("expected the placeholder-safe psql command:\n%s", body)
	}
	if !strings.Contains(body, `class="copyable cli-command env-block"`) {
		t.Fatalf("expected the env-block copy element:\n%s", body)
	}
	if !strings.Contains(body, "DATABASE_URL=postgres://appuser:") {
		t.Fatalf("expected the env block's DATABASE_URL line:\n%s", body)
	}
}
