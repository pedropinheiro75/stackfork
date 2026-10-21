package dashboard_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

const runsAPIHost = "127.0.0.1:55511"

func getRuns(srv *dashboard.Server, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = runsAPIHost
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// recordRun writes one failed run with a seed log into the fixture instance.
func recordRun(t *testing.T, newBase, project string) {
	t.Helper()
	r, err := runs.Begin(filepath.Join(newBase, project), runs.Meta{Command: "start-env", Trigger: "cli", Mode: "container", Project: project})
	if err != nil {
		t.Fatal(err)
	}
	up := r.Step("up", "compose-up", "compose up")
	io.WriteString(up.Start(), "starting\n")
	up.End(nil, 0)
	seed := r.Step("seed", "exec", "bun run db:seed")
	io.WriteString(seed.Start(), "seeding\nerror: duplicate key\n")
	err = errors.New("exited with status 1")
	seed.End(err, 1)
	r.Finish(err)
}

func TestRunsAPIListGetAndLog(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	recordRun(t, newBase, project)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55511)
	if err != nil {
		t.Fatal(err)
	}
	rec := getRuns(srv, "GET", "/api/runs?project="+project)
	var list []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) != 1 || list[0]["id"] != float64(1) || list[0]["outcome"] != "failed" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = getRuns(srv, "GET", "/api/runs?project="+project+"&id=1")
	var one map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &one) != nil || len(one["steps"].([]any)) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = getRuns(srv, "GET", "/api/runs/log?project="+project+"&id=1&step=seed")
	if rec.Code != 200 || rec.Body.String() != "seeding\nerror: duplicate key\nerror: exited with status 1\n" ||
		rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("%d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
}

func TestRunsAPIRefusals(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	recordRun(t, newBase, project)
	// a file the step parameter must never reach
	os.WriteFile(filepath.Join(newBase, project, "secret.log"), []byte("do not serve"), 0o600)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55511)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, method, path string
		want               int
	}{
		{"unknown project", "GET", "/api/runs?project=sf-nope-0123456789ab", 404},
		{"no project", "GET", "/api/runs", 404},
		{"traversal in project", "GET", "/api/runs?project=../" + project, 404},
		{"non-integer id", "GET", "/api/runs?project=" + project + "&id=one", 404},
		{"unknown id", "GET", "/api/runs?project=" + project + "&id=9", 404},
		{"unknown step", "GET", "/api/runs/log?project=" + project + "&id=1&step=nope", 404},
		{"traversal in step", "GET", "/api/runs/log?project=" + project + "&id=1&step=../../manifest", 404},
		{"traversal to a sibling file", "GET", "/api/runs/log?project=" + project + "&id=1&step=../../secret", 404},
		{"step without a log", "GET", "/api/runs/log?project=" + project + "&id=1", 404},
		{"log of a non-integer id", "GET", "/api/runs/log?project=" + project + "&id=x&step=seed", 404},
		{"POST refused", "POST", "/api/runs?project=" + project, 405},
		{"POST log refused", "POST", "/api/runs/log?project=" + project + "&id=1&step=seed", 405},
	}
	for _, c := range cases {
		rec := getRuns(srv, c.method, c.path)
		if rec.Code != c.want {
			t.Errorf("%s: got %d (%s), want %d", c.name, rec.Code, strings.TrimSpace(rec.Body.String()), c.want)
		}
		if strings.Contains(rec.Body.String(), "do not serve") {
			t.Errorf("%s: served a file outside the run", c.name)
		}
	}
	// the Host check applies
	req := httptest.NewRequest("GET", "/api/runs?project="+project, nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 421 {
		t.Errorf("foreign host: %d", rec.Code)
	}
}
