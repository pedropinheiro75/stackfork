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

// TestStackIconUsesAppName: the avatar initials and the card heading come
// from the app (the repo's main checkout folder), never from the worktree
// slug or the instance prefix.
func TestStackIconUsesAppName(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := filepath.Join(t.TempDir(), "export-user-portal")
	if err := os.Rename(fixtureGitRepo(t, "main"), root); err != nil {
		t.Fatal(err)
	}

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

	if !strings.Contains(body, `class="avatar"`) || !strings.Contains(body, `class="stack version lane-`) {
		t.Fatalf("expected a lane avatar element:\n%s", body)
	}
	start := strings.Index(body, `class="avatar"`)
	end := strings.Index(body[start:], "</span>") + start
	iconElement := body[start:end]
	if !strings.HasSuffix(iconElement, ">EU") {
		t.Fatalf("expected the avatar initials EU (from the app folder export-user-portal), got element %q", iconElement)
	}
	if !strings.Contains(body, `class="project-name" id="proj-export-user-portal-h">export-user-portal</h2>`) {
		t.Fatalf("expected the project heading to be the app name:\n%s", body)
	}
	if strings.Count(body, ">export-user-portal<") != 1 {
		t.Fatalf("the app name must appear once, not on every version:\n%s", body)
	}
}
