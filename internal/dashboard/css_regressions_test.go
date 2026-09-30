package dashboard_test

import (
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
		"border-radius: 999px",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("expected app.css to contain %q (copy-action styling)", want)
		}
	}
}
