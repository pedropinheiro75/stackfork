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

// TestAppStripVisibleWhenCollapsedAndProxyShortcut covers task 3.9: the App
// strip renders outside .stack-body so it stays visible on a collapsed
// (stopped-by-default) stack, and the header shows a proxy-start shortcut
// when the proxy isn't running.
func TestAppStripVisibleWhenCollapsedAndProxyShortcut(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")

	project := "sf-appstrip-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
		HTTPPorts: []int{3000},
		Ports:     []state.PortAlloc{{Service: "web", Target: 3000, Host: 20060}},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{"services": map[string]interface{}{
		"web": map[string]interface{}{"image": "node:22"},
	}}
	// Give the service a declared port so it produces an app link.
	config["services"].(map[string]interface{})["web"].(map[string]interface{})["ports"] = []interface{}{
		map[string]interface{}{"target": 3000, "published": "20060", "protocol": "tcp"},
	}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}

	srv, err := dashboard.New(newBase, legacyBase, "test", 55950)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55950"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	stackStart := strings.Index(body, `data-project="`+project+`"`)
	if stackStart < 0 {
		t.Fatalf("stack not found:\n%s", body)
	}
	bodyStart := strings.Index(body[stackStart:], `class="stack-body"`) + stackStart
	stripStart := strings.Index(body[stackStart:], `class="app-strip"`) + stackStart
	if stripStart < stackStart {
		t.Fatalf("expected an app-strip for the stack:\n%s", body)
	}
	if stripStart > bodyStart {
		t.Fatalf("expected the app-strip before .stack-body (so it survives collapse), got strip at %d, body at %d", stripStart, bodyStart)
	}
	// The chip's label is just "App" (maintainer feedback: the address
	// already shows in full below, in the expanded service row) - the
	// full URL still has to reach href/title so the click and hover both
	// carry it.
	strip := body[stackStart:bodyStart]
	if !strings.Contains(strip, `href="http://`+project+`.localhost:20060"`) {
		t.Fatalf("expected the App chip's href to carry the full URL:\n%s", strip)
	}
	if !strings.Contains(strip, `title="http://`+project+`.localhost:20060"`) {
		t.Fatalf("expected the App chip's title to carry the full URL:\n%s", strip)
	}
	if !strings.Contains(strip, `class="app-chip">App</a>`) && !strings.Contains(strip, `>App</a>`) {
		t.Fatalf("expected the App chip's visible text to be just the label:\n%s", strip)
	}
	if strings.Contains(strip, `>App: http://`) {
		t.Fatalf("expected the App chip to no longer show the URL as visible text:\n%s", strip)
	}

	if !strings.Contains(body, `id="proxy-start"`) {
		t.Fatalf("expected the proxy-start shortcut button:\n%s", body)
	}
	startIdx := strings.Index(body, `id="proxy-start"`)
	endIdx := strings.Index(body[startIdx:], ">") + startIdx
	if strings.Contains(body[startIdx:endIdx], "hidden") {
		t.Fatalf("expected the proxy-start button visible (not hidden) since the proxy isn't running:\n%s", body[startIdx:endIdx+1])
	}
}
