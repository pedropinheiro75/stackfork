package dashboard_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// prepareWithSecret re-prepares an already-prepared fixture instance with a
// named secret (env:SF_TEST_DASHBOARD_LOGS_CANARY), so its manifest carries
// a reference the dashboard's log redaction can resolve.
func prepareWithSecret(t *testing.T, newBase, project string) string {
	t.Helper()
	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(m.Root, "compose.yaml")
	var out, errBuf bytes.Buffer
	code := cli.Run([]string{"--root", m.Root, "--state-dir", newBase,
		"--secret", "npm_token=env:SF_TEST_DASHBOARD_LOGS_CANARY",
		"prepare", "-f", composePath}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("prepare with secret failed: %s", errBuf.String())
	}
	return m.Root
}

func getLogs(t *testing.T, srv *dashboard.Server, host, project, extraQuery string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/logs?project=" + project
	if extraQuery != "" {
		url += "&" + extraQuery
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestLogsTailDefault covers task 4.3's plain (non-follow) tail request.
func TestLogsTailDefault(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	t.Setenv("DOCKER_FAKE_LOGS", "web-1  | listening on :3000\n")
	srv, err := dashboard.New(newBase, legacyBase, "test", 55401)
	if err != nil {
		t.Fatal(err)
	}
	rec := getLogs(t, srv, "127.0.0.1:55401", project, "")
	if rec.Code != 200 {
		t.Fatalf("GET /api/logs: status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "listening on :3000") {
		t.Fatalf("expected log output, got %q", rec.Body.String())
	}
}

// TestLogsCanarySecretRedacted covers task 4.3's check: a canary secret in
// log output is redacted the same way `stackfork logs` redacts it.
func TestLogsCanarySecretRedacted(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	const canary = "canary-dashboard-logs-4-3"
	t.Setenv("SF_TEST_DASHBOARD_LOGS_CANARY", canary)

	// Re-prepare with a named secret so the manifest carries a reference
	// to resolve at log time (mirrors internal/cli's own canary test).
	prepareWithSecret(t, newBase, project)

	t.Setenv("DOCKER_FAKE_LOGS", "app-1  | token is "+canary+"\n")
	srv, err := dashboard.New(newBase, legacyBase, "test", 55402)
	if err != nil {
		t.Fatal(err)
	}
	rec := getLogs(t, srv, "127.0.0.1:55402", project, "")
	if rec.Code != 200 {
		t.Fatalf("GET /api/logs: status %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("logs leaked the secret value: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "[redacted:npm_token]") {
		t.Fatalf("expected a redaction marker, got %q", rec.Body.String())
	}
}

// TestLogsMissingProjectRejected covers the required `project` query param.
func TestLogsMissingProjectRejected(t *testing.T) {
	newBase, legacyBase, _ := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55403)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/logs", nil)
	req.Host = "127.0.0.1:55403"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("expected 400 without project, got %d", rec.Code)
	}
}

// TestLogsMaxConcurrentStreams covers task 4.3's cap: an 11th concurrent
// follow request gets 429 while 10 are already streaming.
func TestLogsMaxConcurrentStreams(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	t.Setenv("DOCKER_FAKE_SLEEP", "0.5")
	srv, err := dashboard.New(newBase, legacyBase, "test", 55404)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55404"

	var wg sync.WaitGroup
	codes := make([]int, 11)
	ctxs := make([]context.CancelFunc, 11)
	for i := 0; i < 11; i++ {
		wg.Add(1)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ctxs[i] = cancel
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/api/logs?project="+project+"&follow=1", nil)
			req.Host = host
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	// Let all 11 requests get past the query parsing and into the
	// semaphore check before any of them can finish their (slow) sleep.
	time.Sleep(100 * time.Millisecond)
	wg.Wait()
	for _, cancel := range ctxs {
		cancel()
	}

	rejected := 0
	for _, c := range codes {
		if c == 429 {
			rejected++
		}
	}
	if rejected == 0 {
		t.Fatalf("expected at least one 429 among 11 concurrent streams (cap 10), got codes=%v", codes)
	}
}
