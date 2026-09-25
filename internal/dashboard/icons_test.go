package dashboard_test

import (
	"strings"
	"testing"
)

// TestIconsEmbeddedAndServedSameOrigin covers task 3.5: every logo file
// named in dashboard.go's CSS (one per known Service.Kind, plus the
// fallback) is embedded and reachable at /static/icons/*, same-origin, and
// the D5 response headers - CSP included, unchanged - still apply to that
// route like any other.
func TestIconsEmbeddedAndServedSameOrigin(t *testing.T) {
	srv := newTestServer(t)
	host := "127.0.0.1:55123"

	kinds := []string{
		"postgres", "mysql", "mariadb", "redis", "mongo", "neo4j", "nginx",
		"caddy", "node", "bun", "python", "go", "rabbitmq", "minio", "fallback",
	}
	for _, kind := range kinds {
		rec := doRequest(srv, "GET", "/static/icons/"+kind+".svg", host, nil)
		if rec.Code != 200 {
			t.Errorf("GET /static/icons/%s.svg: status %d", kind, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<svg") {
			n := len(body)
			if n > 60 {
				n = 60
			}
			t.Errorf("icons/%s.svg does not look like an SVG: %q", kind, body[:n])
		}
		if strings.Contains(strings.ToLower(body), "<script") {
			t.Errorf("icons/%s.svg contains a <script> tag", kind)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
			t.Errorf("icons/%s.svg: CSP header = %q, want unchanged \"default-src 'self'\"", kind, got)
		}
	}
}
