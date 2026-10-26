package dashboard_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// projectFixture is a repo "acme" with a recorded container+hybrid plan, a
// worktree on feature/x, both prepared, each with a run.
func projectFixture(t *testing.T) (e *browserEnv, repo, wt string) {
	t.Helper()
	e = newBrowserEnv(t)
	repo = e.readyRepo("acme")
	c, h := []string{"container"}, []string{"hybrid"}
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, AppService: "app", Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp, Modes: c},
		{ID: "up-deps", Kind: knowledge.KindComposeUp, Modes: h, Exclude: []string{"app"}},
		{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "app", Cmd: "npm run db:migrate", Modes: c},
		{ID: "web", Kind: knowledge.KindHost, Name: "web", Cmd: "npm run dev", Port: 5173, Modes: h},
	}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "branch", "backup/old")
	wt = filepath.Join(repo, ".worktrees", "feature-x")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feature/x", wt)
	os.WriteFile(filepath.Join(wt, "f"), []byte("changed"), 0o644)
	for i, dir := range []string{repo, wt} {
		p := []string{"sf-acme-1", "sf-acme-feature-2"}[i]
		branch := []string{"main", "feature/x"}[i]
		idir := filepath.Join(e.newDir, p)
		if err := os.MkdirAll(idir, 0o700); err != nil {
			t.Fatal(err)
		}
		m := &state.Manifest{Project: p, Root: dir, Branch: branch, Hostname: p + ".localhost", Files: []string{"compose.yaml"}, Inputs: map[string]string{}}
		if err := state.WritePrivate(filepath.Join(idir, "manifest.json"), m); err != nil {
			t.Fatal(err)
		}
		if err := state.WritePrivate(filepath.Join(idir, "compose.json"), map[string]any{"services": map[string]any{"app": map[string]any{"image": "node:22"}}}); err != nil {
			t.Fatal(err)
		}
		r, err := runs.Begin(filepath.Join(e.newDir, p), runs.Meta{Command: "start-env", Trigger: "cli", Mode: []string{"container", "hybrid"}[i], Project: p})
		if err != nil {
			t.Fatal(err)
		}
		up := r.Step("up", "compose-up", "compose up")
		up.Start()
		up.End(nil, 0)
		var runErr error
		if i == 1 {
			runErr = errors.New("exited with status 1")
		}
		step := r.Step("migrate", "exec", "npm run db:migrate")
		io.WriteString(step.Start(), "boom\n")
		step.End(runErr, 1)
		r.Finish(runErr)
	}
	return e, repo, wt
}

func TestProjectReadModel(t *testing.T) {
	e, repo, wt := projectFixture(t)
	rec := e.get("/api/project?name=acme")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	t.Log(rec.Body.String())
	var p struct {
		Name, Path    string
		DefaultBranch string `json:"default_branch"`
		DefaultMode   string `json:"default_mode"`
		Modes         []string
		Knowledge     struct{ State, From string }
		Live          struct{ State string }
		Entry         struct{ State string }
		Checkouts     []struct {
			Key, Path, Mode, State, Project string
			Main, Changes                   bool
			LastRun                         *struct {
				ID            int
				Outcome, Mode string
				FailedStep    string `json:"failed_step"`
				Steps         map[string]struct{ State string }
			} `json:"last_run"`
		}
		Branches []struct{ Branch, Upstream string } `json:"branches_not_checked_out"`
		Workflow map[string]struct {
			Nodes []struct {
				ID     string
				InMode bool `json:"in_mode"`
			}
		}
		Config struct {
			Secrets       []any
			EnvOverrides  []any  `json:"env_overrides"`
			BranchCleanup string `json:"branch_cleanup"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "acme" || p.Path != repo || p.DefaultBranch != "main" || p.DefaultMode != "container" || strings.Join(p.Modes, ",") != "container,hybrid" {
		t.Errorf("header: %+v", p)
	}
	if p.Knowledge.State != "verified" || p.Entry.State == "" || p.Live.State != "off" || p.Config.BranchCleanup != "off" {
		t.Errorf("state: %+v", p)
	}
	if len(p.Checkouts) != 2 || !p.Checkouts[0].Main || p.Checkouts[0].Key != "main" || p.Checkouts[1].Key != "feature/x" || p.Checkouts[1].Path != wt {
		t.Fatalf("checkouts: %+v", p.Checkouts)
	}
	main, feat := p.Checkouts[0], p.Checkouts[1]
	if feat.Changes != true || main.Changes || main.Project == "" || feat.State == "" || main.State == "no-stack" {
		t.Errorf("checkouts: %+v", p.Checkouts)
	}
	if main.LastRun == nil || main.LastRun.Outcome != "succeeded" || main.LastRun.Mode != "container" || main.LastRun.Steps["migrate"].State != "succeeded" || main.Mode != "container" {
		t.Errorf("main run: %+v", main.LastRun)
	}
	if feat.LastRun == nil || feat.LastRun.Outcome != "failed" || feat.LastRun.FailedStep != "migrate" || feat.Mode != "hybrid" {
		t.Errorf("feature run: %+v", feat.LastRun)
	}
	if len(p.Branches) != 1 || p.Branches[0].Branch != "backup/old" || p.Branches[0].Upstream != "none" {
		t.Errorf("branches: %+v", p.Branches)
	}
	for _, mode := range []string{"container", "hybrid"} {
		if len(p.Workflow[mode].Nodes) == 0 {
			t.Errorf("no %s graph", mode)
		}
	}
	if rec2 := e.get("/api/project?name=acme"); rec2.Body.String() != rec.Body.String() {
		t.Error("the response is not deterministic")
	}
}

func TestProjectUnknownIs404(t *testing.T) {
	e := newBrowserEnv(t)
	e.readyRepo("acme")
	for _, u := range []string{"/api/project?name=nope", "/api/project", "/api/project?name=acme&path=/elsewhere"} {
		rec := e.get(u)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", u, rec.Code, rec.Body.String())
		}
	}
}

// Each step node carries its last runs for the selected checkout (main by
// default), and a failed step carries the tail of its log.
func TestProjectRecentRunsAndLogTail(t *testing.T) {
	e, _, _ := projectFixture(t)
	type resp struct {
		Checkouts []struct {
			Key     string
			LastRun *struct {
				Steps map[string]struct {
					State   string
					LogTail string `json:"log_tail"`
				}
			} `json:"last_run"`
		}
		Workflow map[string]struct {
			Nodes []struct {
				ID         string
				RecentRuns []struct {
					ID    int
					State string
				} `json:"recent_runs"`
			}
		}
	}
	get := func(u string) resp {
		var p resp
		if err := json.Unmarshal(e.get(u).Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	recent := func(p resp, id string) string {
		for _, n := range p.Workflow["container"].Nodes {
			if n.ID == id && len(n.RecentRuns) == 1 {
				return n.RecentRuns[0].State
			}
		}
		return ""
	}
	p := get("/api/project?name=acme")
	if got := recent(p, "migrate"); got != "succeeded" {
		t.Errorf("main recent run of migrate = %q", got)
	}
	if st := p.Checkouts[1].LastRun.Steps["migrate"]; st.State != "failed" || !strings.HasPrefix(st.LogTail, "boom") {
		t.Errorf("failed step: %+v", st)
	}
	if st := p.Checkouts[0].LastRun.Steps["migrate"]; st.LogTail != "" {
		t.Errorf("a step that passed has no log tail: %+v", st)
	}
	if got := recent(get("/api/project?name=acme&checkout=feature/x"), "migrate"); got != "failed" {
		t.Errorf("feature recent run of migrate = %q", got)
	}
}

// The Repos list links to the project by an encoded name and the repo path, so
// two repos with one name, and names with % or ?, open the right page.
func TestReposLinkEncodesNameAndPath(t *testing.T) {
	e := newBrowserEnv(t)
	odd := e.readyRepo("50%?odd")
	frag := e.get("/fragments/repos").Body.String()
	want := `#project/50%25%3Fodd?path=` + url.QueryEscape(odd)
	if n := strings.Count(frag, `href="`+want+`"`); n != 2 {
		t.Errorf("want 2 links (name and Open project) to %s, got %d", want, n)
	}
}

// A name two repos share is ambiguous for both routes.
func TestProjectResourcesAmbiguousIs409(t *testing.T) {
	e := newBrowserEnv(t)
	e.readyRepo("dup")
	e.readyRepo("a/dup")
	for _, u := range []string{"/api/project?name=dup", "/api/project/resources?name=dup&range=1h"} {
		if rec := e.get(u); rec.Code != 409 {
			t.Errorf("%s: %d %s", u, rec.Code, rec.Body.String())
		}
	}
}
