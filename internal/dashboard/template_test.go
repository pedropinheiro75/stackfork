package dashboard_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// fixtureGitRepo creates a real checkout on the given branch, so the
// inventory's checkout/branch existence checks see it as present.
func fixtureGitRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", branch)
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false") // never wait on this machine's commit-signing agent for a throwaway fixture repo
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-q", "-m", "init")
	return dir
}

// writeFixtureInstance writes a minimal manifest.json + compose.json for
// one instance directly (no `prepare`/no Docker), so template tests can
// point the dashboard at a known fixture.
func writeFixtureInstance(t *testing.T, base, project, root, branch, service string) {
	t.Helper()
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: branch, Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{"services": map[string]interface{}{service: map[string]interface{}{}}}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}
}

// TestIndexRendersFixtureAndEscapesHostileNames covers task 3.1: the
// server-rendered page shows a fixture instance's checkout, branch,
// services and endpoints, and html/template's auto-escaping keeps a
// hostile service name from breaking out of its element.
func TestIndexRendersFixtureAndEscapesHostileNames(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()

	const hostileService = `<script>alert(1)</script>`
	root := fixtureGitRepo(t, "main")
	writeFixtureInstance(t, newBase, "sf-hostile-1", root, "main", hostileService)

	srv, err := dashboard.New(newBase, legacyBase, "test", 55199)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55199"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("hostile service name was not escaped:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("expected the escaped hostile name to appear, got:\n%s", body)
	}
	if !strings.Contains(body, root) {
		t.Fatalf("expected the checkout root to appear:\n%s", body)
	}
	if !strings.Contains(body, "sf-hostile-1.localhost") {
		t.Fatalf("expected the instance hostname to appear:\n%s", body)
	}
}

// Every focusable element in a card carries a data-key, so a poll's
// re-render can hand keyboard focus back to it.
func TestEveryFocusableInCardHasDataKey(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"db:migrate":"m","db:seed":"s"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	project := "sf-focuskeys-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	no := false
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{}, Seeded: &no,
		HTTPPorts: []int{3000}, Ports: []state.PortAlloc{{Service: "web", Target: 3000, Host: 20111}},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, state.SeedLog), []byte("boom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{"services": map[string]interface{}{
		"web": map[string]interface{}{"image": "node:22", "ports": []interface{}{
			map[string]interface{}{"target": 3000, "published": "20111", "protocol": "tcp"}}},
		"db": map[string]interface{}{"image": "postgres:17",
			"ports":       []interface{}{map[string]interface{}{"target": 5432, "published": "20112", "protocol": "tcp"}},
			"environment": map[string]interface{}{"POSTGRES_USER": "u", "POSTGRES_DB": "d", "POSTGRES_PASSWORD": "x"}},
	}}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}
	labels := func(s string) string {
		return "com.docker.compose.project=" + project + ",com.docker.compose.service=" + s + ",io.stackfork.root=/x"
	}
	var ps []string
	for _, s := range []string{"web", "db"} {
		b, _ := json.Marshal(map[string]string{"Labels": labels(s), "State": "running", "Status": "Up", "Ports": ""})
		ps = append(ps, string(b))
	}
	t.Setenv("DOCKER_FAKE_PS", strings.Join(ps, "\n")+"\n")

	srv, err := dashboard.New(newBase, legacyBase, "test", 55971)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55971"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	start := strings.Index(body, `<article class="stack`)
	card := body[start : start+strings.Index(body[start:], "</article>")]
	for _, want := range []string{"endpoint-link", "connection-url", "use-command", "app-chip", "mono-log"} {
		if !strings.Contains(card, want) {
			t.Fatalf("fixture lacks a %s to check", want)
		}
	}
	focusable := regexp.MustCompile(`<(?:a|button)\b[^>]*>|<[a-z]+\b[^>]*\btabindex=[^>]*>`)
	n := 0
	for _, tag := range focusable.FindAllString(card, -1) {
		if strings.HasPrefix(tag, "<a ") && !strings.Contains(tag, "href=") {
			continue
		}
		n++
		if !strings.Contains(tag, ` data-key="`) {
			t.Errorf("focusable element without data-key: %s", tag)
		}
	}
	if n < 10 {
		t.Errorf("only %d focusable elements found; the regexp is wrong", n)
	}
}
