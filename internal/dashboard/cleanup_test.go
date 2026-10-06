package dashboard_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func cleanupInstance(t *testing.T, base, project, root string) {
	t.Helper()
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{}}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]interface{}{"services": map[string]interface{}{"web": map[string]interface{}{}}}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), cfg); err != nil {
		t.Fatal(err)
	}
}

// The panel is read-only: /api/cleanup only counts, and the page has the
// three copyable commands wired to nothing that deletes.
func TestCleanupCountsAndReadOnly(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	live := filepath.Join(t.TempDir(), "live")
	if out, err := exec.Command("git", "init", "-q", "-b", "main", live).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	cleanupInstance(t, newBase, "sf-live-1", live)
	cleanupInstance(t, newBase, "sf-gone-1", filepath.Join(t.TempDir(), "gone"))
	srv, err := dashboard.New(newBase, legacyBase, "test", 55123)
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(srv, "GET", "/api/cleanup", "127.0.0.1:55123", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got map[string]struct {
		Count   int    `json:"count"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["stopped"].Count != 2 || got["orphaned"].Count != 1 || got["all"].Count != 2 {
		t.Fatalf("counts: %+v", got)
	}
	if got["stopped"].Command != "stackfork prune --stopped" || got["orphaned"].Command != "stackfork prune --orphaned" ||
		got["all"].Command != "stackfork prune --all" {
		t.Fatalf("commands: %+v", got)
	}

	if rec := doRequest(srv, "POST", "/api/cleanup", "127.0.0.1:55123", nil); rec.Code == 200 {
		t.Fatal("/api/cleanup must be GET-only")
	}
	page := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	js := doRequest(srv, "GET", "/static/app.js", "127.0.0.1:55123", nil).Body.String()
	if !strings.Contains(page, "Clean up") || !strings.Contains(page, `id="cleanup-toggle"`) {
		t.Fatal("header menu item missing")
	}
	if strings.Contains(js, "prune-run") || strings.Contains(js, `"prune"`) {
		t.Fatal("the dashboard must not offer an action that prunes")
	}
}
