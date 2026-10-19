package dashboard_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestStackIconUsesSlugAfterPrefix covers the maintainer's real-machine
// feedback: the stack icon's initial letter (and its color, indirectly)
// come from the project name with its sf-/idv- instance prefix stripped, so
// every legacy instance doesn't render the same "R" - "idv-app-b-..." must
// show "C", not "R".
func TestStackIconUsesSlugAfterPrefix(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")

	project := "idv-app-b-isolated-20260922-071e511abe10"
	dir := filepath.Join(legacyBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: "app-b.localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{"services": map[string]interface{}{}}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}

	srv, err := dashboard.New(newBase, legacyBase, "test", 55801)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55801"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if !strings.Contains(body, `class="avatar"`) || !strings.Contains(body, `class="stack lane-`) {
		t.Fatalf("expected a lane avatar element:\n%s", body)
	}
	start := strings.Index(body, `class="avatar"`)
	end := strings.Index(body[start:], "</span>") + start
	iconElement := body[start:end]
	if !strings.HasSuffix(iconElement, ">C") {
		t.Fatalf("expected the stack icon's initial to be C (from \"app-b\", not the idv- prefix), got element %q", iconElement)
	}
}
