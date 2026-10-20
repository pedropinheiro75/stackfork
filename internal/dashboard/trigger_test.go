package dashboard_test

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

var realHome = os.Getenv("HOME")

// realStartEnv wires the dashboard to the real cli.StartEnv, as `stackfork dashboard` does.
func (e *browserEnv) realStartEnv() {
	// newBrowserEnv points HOME at a temp dir; the real `docker compose config`
	// the fake delegates to needs the real one to find its plugin.
	e.t.Setenv("HOME", realHome)
	e.srv.StartEnv = func(out, errOut io.Writer, checkout, trigger string) error {
		return cli.StartEnv(out, errOut, cli.StartOptions{Checkout: checkout, NewStateBase: e.newDir, LegacyStateBase: e.t.TempDir(), Trigger: trigger})
	}
}

func instanceRuns(t *testing.T, newDir string) []*runs.Run {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(newDir, "*", "runs"))
	if len(dirs) != 1 {
		t.Fatalf("instances with runs: %v", dirs)
	}
	l, err := runs.List(filepath.Dir(dirs[0]))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestReposStartRecordsDashboardTrigger(t *testing.T) {
	e := newBrowserEnv(t)
	e.realStartEnv()
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	rec := e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if _, final := ndjson(t, rec.Body.String()); final["ok"] != true {
		t.Fatalf("%s", rec.Body.String())
	}
	got := instanceRuns(t, e.newDir)
	if len(got) != 1 || got[0].Command != "start-env" || got[0].Trigger != "dashboard" {
		t.Fatalf("%+v", got)
	}
}

// The card's Start without a host step runs `up` itself: its run is the dashboard's too.
func TestCardStartRecordsDashboardTrigger(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55301)
	if err != nil {
		t.Fatal(err)
	}
	if code, resp := postAction(t, srv, "127.0.0.1:55301", actionRequestFixture{Action: "up", Project: project}); code != 200 || !resp.OK {
		t.Fatalf("%d %+v", code, resp)
	}
	got := instanceRuns(t, newBase)
	if len(got) != 1 || got[0].Command != "up" || got[0].Trigger != "dashboard" || got[0].Outcome != runs.Succeeded {
		t.Fatalf("%+v", got)
	}
}

// The card's Start of a plan with a host step goes through StartEnv.
func TestCardHybridStartRecordsDashboardTrigger(t *testing.T) {
	e := newBrowserEnv(t)
	e.realStartEnv()
	repo := e.readyRepo("hy")
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{"hybrid"}, Exclude: []string{"web"}},
		{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "npm run dev"},
	}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	old := lifecycle.HostStart
	lifecycle.HostStart = func(io.Writer, io.Writer, []string, string, string, knowledge.Step) error { return nil }
	defer func() { lifecycle.HostStart = old }()
	if _, err := lifecycle.TrustHere(e.newDir, repo, k.Plan); err != nil {
		t.Fatal(err)
	}
	if code := cli.Run([]string{"config", "set-mode", repo, "hybrid"}, io.Discard, io.Discard); code != 0 {
		t.Fatal("set-mode")
	}
	e.get("/api/repos")
	if rec := e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{}); rec.Code != 200 {
		t.Fatalf("%s", rec.Body.String())
	}
	project := filepath.Base(filepath.Dir(mustGlob(t, filepath.Join(e.newDir, "*", "runs"))))
	code, resp := e.cardAction("up", project)
	if code != 200 || !resp.OK {
		t.Fatalf("%d %+v", code, resp)
	}
	got := instanceRuns(t, e.newDir)
	if len(got) != 2 || got[0].Trigger != "dashboard" || got[1].Trigger != "dashboard" || got[0].Command != "start-env" {
		t.Fatalf("%+v", got)
	}
}

func mustGlob(t *testing.T, pattern string) string {
	t.Helper()
	m, _ := filepath.Glob(pattern)
	if len(m) != 1 {
		t.Fatalf("%s: %v", pattern, m)
	}
	return m[0]
}

func (e *browserEnv) cardAction(action, project string) (int, actionResponseFixture) {
	return postAction(e.t, e.srv, reposHost, actionRequestFixture{Action: action, Project: project})
}

// last_run is the same in `list --json` and /api/instances.
func TestLastRunMatchesListJSON(t *testing.T) {
	old := cli.Version
	cli.Version = "test"
	defer func() { cli.Version = old }()
	newBase, legacyBase, project := preparedFixture(t)
	if code, resp := postAction(t, mustServer(t, newBase, legacyBase), "127.0.0.1:55301", actionRequestFixture{Action: "up", Project: project}); code != 200 || !resp.OK {
		t.Fatalf("%d %+v", code, resp)
	}
	var listOut, listErr strings.Builder
	if code := cli.Run([]string{"--state-dir", newBase, "--legacy-state-dir", legacyBase, "list", "--json"}, &listOut, &listErr); code != 0 {
		t.Fatal(listErr.String())
	}
	srv := mustServer(t, newBase, legacyBase)
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Host = "127.0.0.1:55301"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var fromList, fromAPI map[string]any
	if json.Unmarshal([]byte(listOut.String()), &fromList) != nil || json.Unmarshal(rec.Body.Bytes(), &fromAPI) != nil {
		t.Fatalf("%s\n%s", listOut.String(), rec.Body.String())
	}
	first, _ := fromAPI["instances"].([]any)[0].(map[string]any)
	lr, _ := first["last_run"].(map[string]any)
	if lr == nil || lr["id"] != float64(1) || lr["command"] != "up" || lr["trigger"] != "dashboard" || lr["outcome"] != "succeeded" {
		t.Fatalf("%v", first)
	}
	if !reflect.DeepEqual(fromList, fromAPI) {
		t.Fatalf("list --json and /api/instances disagree:\n%s\n%s", listOut.String(), rec.Body.String())
	}
}

func mustServer(t *testing.T, newBase, legacyBase string) *dashboard.Server {
	t.Helper()
	srv, err := dashboard.New(newBase, legacyBase, "test", 55301)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}
