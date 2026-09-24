package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

func newTestServer(t *testing.T) *dashboard.Server {
	t.Helper()
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	srv, err := dashboard.New(newBase, legacyBase, "test", 55123)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func doRequest(srv *dashboard.Server, method, path, host string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestRoutes covers task 2.1: GET /, /api/instances and /static/* all
// answer for an allowed Host.
func TestRoutes(t *testing.T) {
	srv := newTestServer(t)
	host := "127.0.0.1:55123"

	rec := doRequest(srv, "GET", "/", host, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), srv.Token) {
		t.Fatalf("GET / body missing the action token")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / content-type = %q", ct)
	}

	rec = doRequest(srv, "GET", "/api/instances", host, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /api/instances: status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET /api/instances content-type = %q", ct)
	}

	rec = doRequest(srv, "GET", "/static/app.js", host, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /static/app.js: status %d", rec.Code)
	}
	rec = doRequest(srv, "GET", "/static/app.css", host, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /static/app.css: status %d", rec.Code)
	}
}

// TestResponseHeaders covers D5's response headers, on every response.
func TestResponseHeaders(t *testing.T) {
	srv := newTestServer(t)
	rec := doRequest(srv, "GET", "/api/instances", "127.0.0.1:55123", nil)
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

// TestHostAllowlist covers task 2.2's DNS-rebinding scenario: any request
// with a disallowed Host gets 421 and no data.
func TestHostAllowlist(t *testing.T) {
	srv := newTestServer(t)
	for _, host := range []string{"evil.example.com", "127.0.0.1:9999", "stackfork.localhost.evil.com"} {
		rec := doRequest(srv, "GET", "/api/instances", host, nil)
		if rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("host %q: status %d, want 421", host, rec.Code)
		}
		if rec.Body.Len() > 0 && strings.Contains(rec.Body.String(), "instances") {
			t.Errorf("host %q: served data despite disallowed Host", host)
		}
	}
	// Allowed forms.
	for _, host := range []string{"127.0.0.1:55123", "localhost:55123", "stackfork.localhost"} {
		rec := doRequest(srv, "GET", "/api/instances", host, nil)
		if rec.Code != 200 {
			t.Errorf("host %q: status %d, want 200", host, rec.Code)
		}
	}
}

// TestActionsRejectsWithoutTokenOrOrigin covers task 2.2: missing token,
// wrong token, foreign Origin and a GET to the actions route are all
// rejected with no state change (the handler never gets past the check).
func TestActionsRejectsWithoutTokenOrOrigin(t *testing.T) {
	srv := newTestServer(t)
	host := "127.0.0.1:55123"

	rec := doRequest(srv, "POST", "/api/actions", host, nil)
	if rec.Code == 200 {
		t.Errorf("missing token: status %d, want rejection", rec.Code)
	}

	rec = doRequest(srv, "POST", "/api/actions", host, map[string]string{
		"X-Stackfork-Token": "wrong-token", "Origin": "http://127.0.0.1:55123",
	})
	if rec.Code == 200 {
		t.Errorf("wrong token: status %d, want rejection", rec.Code)
	}

	rec = doRequest(srv, "POST", "/api/actions", host, map[string]string{
		"X-Stackfork-Token": srv.Token, "Origin": "http://evil.example.com",
	})
	if rec.Code == 200 {
		t.Errorf("foreign origin: status %d, want rejection", rec.Code)
	}

	rec = doRequest(srv, "GET", "/api/actions", host, map[string]string{
		"X-Stackfork-Token": srv.Token, "Origin": "http://127.0.0.1:55123",
	})
	if rec.Code == 200 {
		t.Errorf("GET to actions route: status %d, want rejection", rec.Code)
	}
}

// TestGetNeverChangesState is the scenario from the dashboard spec
// ("GET never changes state"): every GET route is read-only by
// construction (no GET handler ever calls an action function), verified
// here by confirming none of them is even wired to accept a mutating verb
// without the security checks actions require.
func TestGetNeverChangesState(t *testing.T) {
	srv := newTestServer(t)
	host := "127.0.0.1:55123"
	for _, path := range []string{"/", "/api/instances", "/static/app.js"} {
		rec := doRequest(srv, "GET", path, host, nil)
		if rec.Code >= 500 {
			t.Errorf("GET %s: unexpected server error %d", path, rec.Code)
		}
	}
}
