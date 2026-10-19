package dashboard_test

import (
	"regexp"
	"strings"
	"testing"
)

func section(t *testing.T, body, open, close string) string {
	t.Helper()
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatalf("%s missing", open)
	}
	j := strings.Index(body[i:], close)
	if j < 0 {
		t.Fatalf("%s not closed", open)
	}
	return body[i : i+j]
}

func TestCleanupViewShowsDiskBarAndOnePrimary(t *testing.T) {
	srv := newTestServer(t)
	body := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	v := section(t, body, `<main id="view-cleanup"`, "</main>")
	if n := strings.Count(v, "btn-primary"); n != 1 {
		t.Fatalf("want exactly one .btn-primary in Clean up, got %d:\n%s", n, v)
	}
	if !regexp.MustCompile(`btn-primary[^>]*free-space-btn[^>]*>Free Docker space<|free-space-btn[^>]*btn-primary[^>]*>Free Docker space<`).MatchString(v) &&
		!regexp.MustCompile(`class="[^"]*btn-primary[^"]*"[^>]*>Free Docker space<`).MatchString(v) {
		t.Errorf("the primary must be Free Docker space:\n%s", v)
	}
	if !strings.Contains(v, `role="meter"`) || !strings.Contains(v, `id="disk-meter"`) {
		t.Errorf("disk gauge must be a role=meter element:\n%s", v)
	}
	if strings.Contains(v, `class="btn run-btn`) || strings.Contains(v, "btn-danger") {
		t.Errorf("no red or primary prune buttons:\n%s", v)
	}
}

func TestSettingsFragmentOnePrimaryAndGhostRemove(t *testing.T) {
	e := newBrowserEnv(t)
	s := e.get("/fragments/settings").Body.String()
	if n := strings.Count(s, "btn-primary"); n != 1 || !regexp.MustCompile(`btn-primary[^>]*>Add folder<`).MatchString(s) {
		t.Fatalf("Settings has one primary, Add folder (got %d):\n%s", n, s)
	}
	if !regexp.MustCompile(`class="btn btn-ghost"[^>]*data-config-op="remove-root"`).MatchString(s) {
		t.Errorf("folder rows use a ghost Remove:\n%s", s)
	}
	if strings.Contains(s, "repo-btn") {
		t.Errorf("legacy repo-btn left in settings:\n%s", s)
	}
}

func TestLogDrawerAndLearnModalMarkup(t *testing.T) {
	srv := newTestServer(t)
	body := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	d := section(t, body, `id="logs-panel"`, `id="logs-body"`)
	for _, want := range []string{`role="dialog"`, `aria-modal="false"`, `aria-labelledby="logs-title"`, `id="logs-title"`, `id="logs-close"`} {
		if !strings.Contains(d, want) {
			t.Errorf("drawer lacks %s:\n%s", want, d)
		}
	}
	if !regexp.MustCompile(`id="logs-body"[^>]*class="mono-log"|class="mono-log"[^>]*id="logs-body"`).MatchString(body) {
		t.Error("log body must be .mono-log")
	}
	m := section(t, body, `<dialog id="learn-modal"`, "</dialog>")
	for _, want := range []string{`aria-labelledby="learn-title"`, `id="learn-close"`, `id="learn-start"`, `btn-primary`, `id="learn-agents"`, `id="learn-copy"`} {
		if !strings.Contains(m, want) {
			t.Errorf("modal lacks %s:\n%s", want, m)
		}
	}
	if strings.Contains(m, "repo-btn") {
		t.Error("legacy repo-btn in modal")
	}
}

func TestPanelsCSS(t *testing.T) {
	srv := newTestServer(t)
	css := doRequest(srv, "GET", "/static/app.css", "127.0.0.1:55123", nil).Body.String()
	for _, want := range []string{"var(--shadow-modal)", "var(--surface-3)", "var(--radius-lg)", "#logs-panel", "translateX(100%)", "prefers-reduced-motion"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %q", want)
		}
	}
	if strings.Contains(css, "transition: all") {
		t.Error("transition: all")
	}
}
