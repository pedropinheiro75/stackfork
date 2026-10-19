package dashboard_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestProxyStartButtonHiddenWhenRunning is the regression test for the
// maintainer's screenshot review: the server-rendered template and app.js's
// re-render both already set the "Start proxy" button's `hidden` attribute
// correctly when the proxy is running, but .icon-btn's own `display:
// inline-flex` (an author-stylesheet rule) beat the browser's default
// `[hidden] { display: none }`, so the button stayed visible right next to
// "Proxy: running on <port>" regardless. This only catches a regression of
// the CSS override itself - the actual "is it visible" behaviour needs a
// real browser, verified live when the dashboard screenshots were retaken.
func TestProxyStartButtonHiddenWhenRunning(t *testing.T) {
	srv := newTestServer(t)
	rec := doRequest(srv, "GET", "/static/app.css", "127.0.0.1:55123", nil)
	if rec.Code != 200 {
		t.Fatalf("GET /static/app.css: status %d", rec.Code)
	}
	css := rec.Body.String()
	if !strings.Contains(css, "#proxy-start[hidden]") {
		t.Fatal("expected an explicit #proxy-start[hidden] override so .icon-btn's display rule can't win")
	}
}

// TestCopyActionsStyledAsClickable is the regression test for the
// maintainer's other screenshot finding: copy actions (connection URL,
// psql, stackfork exec, SMTP_HOST/PORT, "Copy .env for host app") rendered
// as plain text with no visual affordance. Every .copyable element now gets
// a copy icon and a hover color shift, and .endpoint-copy/.cli-command are
// shaped as a pill/chip rather than bare text - pinned here so it can't
// silently regress back to plain text.
func TestCopyActionsStyledAsClickable(t *testing.T) {
	srv := newTestServer(t)
	rec := doRequest(srv, "GET", "/static/app.css", "127.0.0.1:55123", nil)
	if rec.Code != 200 {
		t.Fatalf("GET /static/app.css: status %d", rec.Code)
	}
	css := rec.Body.String()
	for _, want := range []string{
		".copyable::before",
		".copyable:hover::before",
		"border-radius: var(--radius-full)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("expected app.css to contain %q (copy-action styling)", want)
		}
	}
}

func TestTokensServedAndComplete(t *testing.T) {
	srv := newTestServer(t)
	rec := doRequest(srv, "GET", "/static/tokens.css", "127.0.0.1:55123", nil)
	if rec.Code != 200 {
		t.Fatalf("GET /static/tokens.css: status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	css := rec.Body.String()
	for _, want := range []string{"--surface-1:#131318", "--surface-1:#FFFFFF", "--lane-8:#4A4AD0", "prefers-color-scheme: light", `[data-theme="light"]`, `:root:not([data-theme="dark"])`, "GeistMono-Variable.woff2"} {
		if !strings.Contains(css, want) {
			t.Errorf("tokens.css missing %q", want)
		}
	}
}

func TestGeistMonoEmbedded(t *testing.T) {
	srv := newTestServer(t)
	rec := doRequest(srv, "GET", "/static/fonts/GeistMono-Variable.woff2", "127.0.0.1:55123", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "wOF2") {
		t.Fatal("body is not a woff2 file")
	}
}

// All colour lives in tokens.css; app.css only references variables.
func TestAppCSSHasNoHardCodedColours(t *testing.T) {
	srv := newTestServer(t)
	rec := doRequest(srv, "GET", "/static/app.css", "127.0.0.1:55123", nil)
	css := rec.Body.String()
	if m := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(css, -1); len(m) > 0 {
		t.Errorf("app.css has hard-coded colours: %v", m)
	}
	if m := regexp.MustCompile(`rgba?\(`).FindAllString(css, -1); len(m) > 0 {
		t.Errorf("app.css has rgb()/rgba() colours: %d", len(m))
	}
}

func TestThemeToggleWired(t *testing.T) {
	srv := newTestServer(t)
	html := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	for _, want := range []string{`/static/tokens.css`, `id="theme-toggle"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index missing %q", want)
		}
	}
	if strings.Index(html, "tokens.css") > strings.Index(html, "app.css") {
		t.Error("tokens.css must load before app.css")
	}
	js := doRequest(srv, "GET", "/static/app.js", "127.0.0.1:55123", nil).Body.String()
	if !strings.Contains(js, "function setTheme(") {
		t.Error("app.js missing setTheme")
	}
}

func TestThemeInitIsExternalScript(t *testing.T) {
	srv := newTestServer(t)
	html := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	if !strings.Contains(html, `src="/static/theme-init.js"`) {
		t.Error("index must load /static/theme-init.js")
	}
	if strings.Index(html, "theme-init.js") > strings.Index(html, "tokens.css") {
		t.Error("theme-init.js must load before the stylesheets")
	}
	if regexp.MustCompile(`<script(\s[^>]*)?>`).FindAllString(html, -1) == nil {
		t.Fatal("no scripts found")
	}
	for _, m := range regexp.MustCompile(`<script[^>]*>`).FindAllString(html, -1) {
		if !strings.Contains(m, "src=") {
			t.Errorf("inline script blocked by CSP: %s", m)
		}
	}
	if rec := doRequest(srv, "GET", "/static/theme-init.js", "127.0.0.1:55123", nil); rec.Code != 200 {
		t.Errorf("theme-init.js status %d", rec.Code)
	}
}

// The filter input must not inherit the system cluster's flex/margin rule,
// and an unconfigured user lands on Settings whatever view is open.
func TestTask2Regressions(t *testing.T) {
	srv := newTestServer(t)
	css := doRequest(srv, "GET", "/static/app.css", "127.0.0.1:55123", nil).Body.String()
	if strings.Contains(css, "#filter,") {
		t.Error("#filter must not share the .system-cluster rule")
	}
	js := doRequest(srv, "GET", "/static/app.js", "127.0.0.1:55123", nil).Body.String()
	if !strings.Contains(js, "autoShownSettings = true;\n          setView(\"settings\");") {
		t.Error("loadRepos must show Settings once, whatever view is open")
	}
}

// TestToastRegionsAndLightStateTokens pins the ActionToast live regions in
// the page shell and the AA-safe light state colours (DESIGN.md Colors).
func TestToastRegionsAndLightStateTokens(t *testing.T) {
	srv := newTestServer(t)
	page := doRequest(srv, "GET", "/", "127.0.0.1:55123", nil).Body.String()
	for _, want := range []string{`id="toast-status" role="status" aria-live="polite"`, `id="toast-alert" role="alert"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %s", want)
		}
	}
	css := doRequest(srv, "GET", "/static/tokens.css", "127.0.0.1:55123", nil).Body.String()
	for _, want := range []string{"--ok:#116A42", "--attention:#7A5200", "--failed:#B01B38", "--busy:#0A6276", "--off:#5F5E73"} {
		if strings.Count(css, want) != 2 {
			t.Errorf("tokens.css should hold %s in both light blocks", want)
		}
	}
}
