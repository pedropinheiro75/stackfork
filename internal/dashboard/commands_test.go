package dashboard_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestIndexShowsCopyableCommandsAndActionWiring covers task 4.4: an
// orphaned card shows `stackfork gc --apply` and `stackfork down
// --instance ... --volumes` as copyable text (never a button that removes
// it), and a running service gets a restart button and a logs button.
func TestIndexShowsCopyableCommandsAndActionWiring(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()

	// Orphaned: manifest points at a checkout that no longer exists.
	project := "sf-orphan-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: filepath.Join(t.TempDir(), "gone"), Branch: "main",
		Hostname: project + ".localhost", Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{"services": map[string]interface{}{"web": map[string]interface{}{}}}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}

	// A second, healthy instance (a real, existing checkout) so its
	// service row has the restart/logs buttons the orphaned card's Services
	// list never gets (inventory.buildInstance only reads services once the
	// checkout and branch both exist).
	healthyProject := "sf-healthy-2"
	healthyRoot := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "-b", "main", healthyRoot).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	hDir := filepath.Join(newBase, healthyProject)
	if err := os.MkdirAll(hDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hm := &state.Manifest{
		Project: healthyProject, Root: healthyRoot, Branch: "main",
		Hostname: healthyProject + ".localhost", Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(hDir, "manifest.json"), hm); err != nil {
		t.Fatal(err)
	}
	if err := state.WritePrivate(filepath.Join(hDir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}

	srv, err := dashboard.New(newBase, legacyBase, "test", 55501)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55501"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if !strings.Contains(body, "stackfork gc") {
		t.Fatalf("expected the orphaned card to show a copyable `stackfork gc` command:\n%s", body)
	}
	if !strings.Contains(body, "stackfork down --instance "+project+" --volumes") {
		t.Fatalf("expected the copyable down --volumes command:\n%s", body)
	}
	if !strings.Contains(body, "cli-command") {
		t.Fatalf("expected the commands to be rendered with the copyable cli-command class:\n%s", body)
	}
	if !strings.Contains(body, `<button class="action" data-action="down" data-project="`+project+`">Down</button>`) {
		t.Fatalf("expected a plain Down button with no --volumes anywhere in its own markup:\n%s", body)
	}
	if !strings.Contains(body, `data-action="restart"`) {
		t.Fatalf("expected a restart button for the web service:\n%s", body)
	}
	if !strings.Contains(body, `class="logs-button"`) {
		t.Fatalf("expected a logs button:\n%s", body)
	}
	if !strings.Contains(body, `id="logs-panel"`) {
		t.Fatalf("expected the logs panel markup:\n%s", body)
	}
}
