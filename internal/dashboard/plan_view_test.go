package dashboard_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// hybridPlan records a hybrid plan (a host step) in a ready repo.
func hybridPlan(t *testing.T, repo, hostID string) knowledge.Plan {
	t.Helper()
	p := knowledge.Plan{Status: knowledge.PlanRecorded, AppService: "app", Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{"container"}},
		{ID: "deps", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}, Modes: []string{"hybrid"}},
		{ID: hostID, Kind: knowledge.KindHost, Name: "web", Cmd: "npm run dev", PortEnv: []string{"PORT"}, Port: 3000, Modes: []string{"hybrid"}},
	}}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: p}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return p
}

func trustFile(e *browserEnv, repo string) string { return knowledge.TrustPath(e.newDir, repo) }

func TestRepoFragmentRendersPlanList(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
	cfg, _ := config.Load()
	cfg.SetRepoMode(repo, "hybrid")
	cfg.Save()
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	for _, want := range []string{`data-step="app"`, "untrusted", `<select`, `data-mode-for="` + repo + `"`,
		`>container</option>`, `>hybrid</option>`, "Trust on this machine", "Host steps changed on this machine",
		`data-run="stackfork --root &#39;` + repo + `&#39; plan --trust"`} {
		if !strings.Contains(frag, want) {
			t.Errorf("missing %q in:\n%s", want, frag)
		}
	}
	i := strings.Index(frag, `class="plan-list"`)
	j := strings.Index(frag[i:], `class="wt-table"`)
	if i < 0 || strings.Contains(frag[i:i+j], "btn-primary") {
		t.Errorf("plan list missing or has a primary button")
	}
	// the skipped step is dimmed, not hidden
	if !strings.Contains(frag, `data-step="up"`) || !strings.Contains(frag, "skipped") || !strings.Contains(frag, `plan-row plan-skipped`) {
		t.Errorf("skipped step not listed")
	}
}

func TestPlanListEscapesHostileStepIDs(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, `<script>alert(1)</script>`)
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	if strings.Contains(frag, "<script>alert(1)") || !strings.Contains(frag, "&lt;script&gt;alert(1)") {
		t.Fatalf("step id not escaped:\n%s", frag)
	}
}

func stubConfirm(t *testing.T, answer bool) *string {
	t.Helper()
	old := dashboard.Confirm
	t.Cleanup(func() { dashboard.Confirm = old })
	asked := new(string)
	dashboard.Confirm = func(title, text string) (bool, error) { *asked = text; return answer, nil }
	return asked
}

func TestRunPlanTrustRequiresConfirm(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
	e.get("/api/repos")
	asked := stubConfirm(t, true)
	cmd := "stackfork --root '" + repo + "' plan --trust"
	rec := e.post("/api/run", map[string]string{"command": cmd}, reqOpts{})
	if rec.Code != 200 || *asked == "" {
		t.Fatalf("%d %s, dialog %q", rec.Code, rec.Body.String(), *asked)
	}
	for _, want := range []string{"stackfork --root " + repo + " plan --trust", "app [host]", "name: web", "cmd: npm run dev", "port_env: PORT", "port: 3000"} {
		if !strings.Contains(*asked, want) {
			t.Errorf("dialog lacks %q:\n%s", want, *asked)
		}
	}
	for _, c := range []string{"stackfork --root '" + repo + "' plan --json", "stackfork --root '" + repo + "' plan", "stackfork plan --trust"} {
		rec := e.post("/api/run", map[string]string{"command": c}, reqOpts{})
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), "only `plan --trust` runs from the page") {
			t.Errorf("%q: %d %s", c, rec.Code, rec.Body.String())
		}
	}
}

func TestConfirmCancelRunsNothing(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
	e.get("/api/repos")
	stubConfirm(t, false)
	rec := e.post("/api/run", map[string]string{"command": "stackfork --root '" + repo + "' plan --trust"}, reqOpts{})
	if !strings.Contains(rec.Body.String(), "cancelled; nothing ran") {
		t.Fatalf("%s", rec.Body.String())
	}
	if _, err := os.Stat(trustFile(e, repo)); err == nil {
		t.Fatal("trust file written after Cancel")
	}
}

// Start on a ready repo with an untrusted host step asks first; Cancel runs
// neither prepare nor up (StartEnv is never called).
func TestStartAsksForTrustFirstAndCancelRunsNothing(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
	e.get("/api/repos")
	asked := stubConfirm(t, false)
	rec := e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if !strings.Contains(*asked, "app [host]") || !strings.Contains(rec.Body.String(), "nothing ran") {
		t.Fatalf("dialog %q, response %s", *asked, rec.Body.String())
	}
	if len(e.started()) != 0 {
		t.Fatalf("started after Cancel: %v", e.started())
	}
	if _, err := os.Stat(trustFile(e, repo)); err == nil {
		t.Fatal("trust file written after Cancel")
	}
	// approve: trust is recorded, then the start runs
	stubConfirm(t, true)
	e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if len(e.started()) != 1 {
		t.Fatalf("starts after approval: %v", e.started())
	}
	if _, err := os.Stat(trustFile(e, repo)); err != nil {
		t.Fatalf("no trust file after approval: %v", err)
	}
	// already trusted: no dialog
	asked = stubConfirm(t, false)
	e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if *asked != "" || len(e.started()) != 2 {
		t.Fatalf("trusted start asked %q, starts %v", *asked, e.started())
	}
	_ = lifecycle.StepBlock
}

func TestConfigSetModeFromPage(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	e.get("/api/repos")
	rec := e.post("/api/config", map[string]string{"op": "set-mode", "value": repo + "=hybrid"}, reqOpts{})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	cfg, _ := config.Load()
	if cfg.ModeFor(repo) != "hybrid" {
		t.Fatalf("mode %q", cfg.ModeFor(repo))
	}
	other := t.TempDir()
	rec = e.post("/api/config", map[string]string{"op": "set-mode", "value": other + "=hybrid"}, reqOpts{})
	if rec.Code != 403 {
		t.Fatalf("unscanned path: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.post("/api/config", map[string]string{"op": "set-mode", "value": repo + "=fast"}, reqOpts{})
	if rec.Code != 400 {
		t.Fatalf("bad mode: %d", rec.Code)
	}
	var resp struct{ Message string }
	json.Unmarshal(rec.Body.Bytes(), &resp)
}
