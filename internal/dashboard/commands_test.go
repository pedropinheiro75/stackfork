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
	if !strings.Contains(body, `class="stack-menu"`) {
		t.Fatalf("expected the destructive commands inside the per-stack \"...\" menu:\n%s", body)
	}
	// The orphaned instance has nothing running (state missing-checkout),
	// so per fix 4 it gets only the Up button, never Down - but wherever a
	// Down button does appear (running/partial stacks), it must carry no
	// --volumes anywhere in its own markup.
	if !strings.Contains(body, `<button class="icon-btn" data-action="up" data-project="`+project+`" title="Up">`) {
		t.Fatalf("expected a plain Up icon button for the orphaned instance:\n%s", body)
	}
	if strings.Contains(body, `data-action="down" data-project="`+project+`"`) {
		t.Fatalf("did not expect a Down button on an instance with nothing running:\n%s", body)
	}
	if idx := strings.Index(body, `data-action="down"`); idx >= 0 {
		section := body[idx : idx+120]
		if strings.Contains(section, "--volumes") {
			t.Fatalf("expected a Down icon button with no --volumes in its own markup, got:\n%s", section)
		}
	}
	if !strings.Contains(body, `data-action="restart"`) {
		t.Fatalf("expected a restart button for the web service:\n%s", body)
	}
	if !strings.Contains(body, `class="icon-btn logs-button"`) {
		t.Fatalf("expected a logs button:\n%s", body)
	}
	if !strings.Contains(body, `id="logs-panel"`) {
		t.Fatalf("expected the logs panel markup:\n%s", body)
	}
	if !strings.Contains(body, `class="service-settings" hidden`) {
		t.Fatalf("expected an expandable settings panel per service:\n%s", body)
	}
	if !strings.Contains(body, `data-toggle-stack`) {
		t.Fatalf("expected the stack header to be collapsible:\n%s", body)
	}

	// The destructive command must appear once, inside the menu, and
	// nowhere else on the card body (maintainer feedback: "the destructive
	// command lives in the '...' menu, not printed on the card").
	menuStart := strings.Index(body, `class="stack-menu"`)
	menuEnd := strings.Index(body[menuStart:], "</div>") + menuStart
	before := body[:menuStart]
	after := body[menuEnd:]
	downVolumes := "stackfork down --instance " + project + " --volumes"
	if strings.Contains(before, downVolumes) || strings.Contains(after, downVolumes) {
		t.Fatalf("expected the down --volumes command only inside the stack menu, found it elsewhere too")
	}
}
