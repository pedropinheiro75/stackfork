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
	"github.com/pedropinheiro75/stackfork/internal/state"
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
		`>container</option>`, `>hybrid</option>`, "Trust on this machine", "not trusted on this machine yet",
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
	setMode(t, repo, "hybrid")
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

func setMode(t *testing.T, repo, mode string) {
	t.Helper()
	cfg, _ := config.Load()
	if err := cfg.SetRepoMode(repo, mode); err != nil {
		t.Fatal(err)
	}
	cfg.Save()
}

// Container mode runs no host step: Start opens no dialog, and the page shows
// a quiet note with Trust in the overflow menu, not a failed callout.
func TestContainerModeStartAsksNothingAndTrustIsQuiet(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
	e.get("/api/repos")
	asked := stubConfirm(t, false)
	e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if *asked != "" || len(e.started()) != 1 {
		t.Fatalf("dialog %q, starts %v", *asked, e.started())
	}
	frag := e.get("/fragments/repos").Body.String()
	if strings.Contains(frag, "data-trust-callout") || !strings.Contains(frag, "needed only in hybrid mode") || !strings.Contains(frag, "plan --trust") {
		t.Fatalf("container mode must show the quiet note:\n%s", frag)
	}
	setMode(t, repo, "hybrid")
	frag = e.get("/fragments/repos").Body.String()
	if !strings.Contains(frag, "data-trust-callout") || strings.Contains(frag, `"id":"app"`) || !strings.Contains(frag, "cmd: npm run dev") {
		t.Fatalf("hybrid mode: callout with readable lines expected:\n%s", frag)
	}
}

func TestConfigSetModeFromPage(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
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

func TestPlanTrustRefusesRepeatedRoot(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
	e.get("/api/repos")
	asked := stubConfirm(t, true)
	for _, c := range []string{
		"stackfork --root '" + repo + "' --root /tmp plan --trust",
		"stackfork --root '" + repo + "' --instance sf-x plan --trust",
	} {
		rec := e.post("/api/run", map[string]string{"command": c}, reqOpts{})
		if rec.Code != 403 || *asked != "" {
			t.Errorf("%q: %d %s dialog %q", c, rec.Code, rec.Body.String(), *asked)
		}
	}
}

// The person approves what the dialog showed, even when knowledge.json
// changes while the dialog is open.
func TestPlanTrustWritesTheShownPlan(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	shown := hybridPlan(t, repo, "app")
	e.get("/api/repos")
	old := dashboard.Confirm
	t.Cleanup(func() { dashboard.Confirm = old })
	dashboard.Confirm = func(title, text string) (bool, error) {
		hybridPlan(t, repo, "evil") // swapped while the dialog is open
		return true, nil
	}
	rec := e.post("/api/run", map[string]string{"command": "stackfork --root '" + repo + "' plan --trust"}, reqOpts{})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	h, _, ok := knowledge.ReadTrust(trustFile(e, repo))
	if !ok || h != knowledge.PlanHash(shown.Steps) {
		t.Fatalf("trust file does not hold the shown plan (ok=%v)", ok)
	}
	k, _ := knowledge.Load(filepath.Join(repo, ".stackfork", "knowledge.json"))
	if trusted, _ := lifecycle.TrustedHere(e.newDir, repo, k.Plan, "hybrid"); trusted {
		t.Fatal("the swapped plan must be untrusted")
	}
}

// Start from a linked worktree shows the plan StartEnv will check: the
// worktree's own knowledge.json first.
func TestWorktreeStartDialogUsesTheWorktreePlan(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hybridPlan(t, repo, "app")
	setMode(t, repo, "hybrid")
	wt := filepath.Join(repo, ".worktrees", "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "wt", wt)
	hybridPlan(t, wt, "wt-app")
	e.get("/api/repos")
	asked := stubConfirm(t, false)
	e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": wt}, reqOpts{})
	if !strings.Contains(*asked, "wt-app [host]") || strings.Contains(*asked, "\napp [host]") {
		t.Fatalf("dialog:\n%s", *asked)
	}
}

func containerOnlyPlan(t *testing.T, repo string) {
	t.Helper()
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSetModeRefusesUnsupportedAndPageShowsConfiguredMode(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	containerOnlyPlan(t, repo)
	e.get("/api/repos")
	rec := e.post("/api/config", map[string]string{"op": "set-mode", "value": repo + "=hybrid"}, reqOpts{})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "supported: container") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	setMode(t, repo, "hybrid") // configured before the plan changed
	frag := e.get("/fragments/repos").Body.String()
	if !strings.Contains(frag, "hybrid not supported by this plan") {
		t.Fatalf("unsupported configured mode not shown:\n%s", frag)
	}
}

func TestTrustCalloutTitles(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	p := hybridPlan(t, repo, "app")
	setMode(t, repo, "hybrid")
	e.get("/api/repos")
	if frag := e.get("/fragments/repos").Body.String(); !strings.Contains(frag, "not trusted on this machine yet") {
		t.Fatalf("never trusted title missing")
	}
	if _, err := lifecycle.TrustHere(e.newDir, repo, p); err != nil {
		t.Fatal(err)
	}
	// reorder: same steps, different order -> hash differs, no content diff
	p.Steps = append(p.Steps[:2:2], knowledge.Step{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"--reassign-ports"}}, p.Steps[2])
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: p}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	frag := e.get("/fragments/repos").Body.String()
	if !strings.Contains(frag, "changed since you trusted them") || !strings.Contains(frag, "&#43; prep [prepare]") {
		t.Fatalf("changed title/diff missing:\n%s", frag)
	}
}

func TestUnloadableKnowledgeShowsFailedCallout(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	if err := os.WriteFile(filepath.Join(repo, ".stackfork", "knowledge.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	if !strings.Contains(frag, "data-plan-error") || !strings.Contains(frag, "callout-failed") || !strings.Contains(frag, "knowledge.json") {
		t.Fatalf("load error not shown:\n%s", frag)
	}
}

// A plan whose approval text does not fit one dialog is never offered for
// approval: no dialog, no trust file, in both the Start and the page paths.
func TestOverLongPlanOffersNoApprovalDialog(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	p := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{"container"}}}}
	for i := 0; i < 200; i++ {
		p.Steps = append(p.Steps, knowledge.Step{ID: "host-" + strings.Repeat("x", 5) + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Kind: knowledge.KindHost, Name: "web", Cmd: "npm run dev", Modes: []string{"hybrid"}})
	}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: p}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	setMode(t, repo, "hybrid")
	e.get("/api/repos")
	asked := stubConfirm(t, true)

	rec := e.post("/api/run", map[string]string{"command": "stackfork --root '" + repo + "' plan --trust"}, reqOpts{})
	if !strings.Contains(rec.Body.String(), "too long to review in a dialog") {
		t.Fatalf("run: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if !strings.Contains(rec.Body.String(), "too long to review in a dialog") {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	if *asked != "" || len(e.started()) != 0 {
		t.Fatalf("dialog %q, starts %v", *asked, e.started())
	}
	if _, err := os.Stat(trustFile(e, repo)); err == nil {
		t.Fatal("trust file written for an unseen plan")
	}
}

// The card's Start on a hybrid instance runs the plan (StartEnv), not a
// plain compose up that would start the app container too.
func TestCardStartOnHybridRunsThePlan(t *testing.T) {
	for _, mode := range []string{"hybrid", "container"} {
		t.Run(mode, func(t *testing.T) {
			e := newBrowserEnv(t)
			repo := e.readyRepo("svc")
			hybridPlan(t, repo, "app")
			cfg, _ := config.Load()
			cfg.SetRepoMode(repo, mode)
			cfg.Save()
			project := "sf-card-start"
			os.MkdirAll(filepath.Join(e.newDir, project), 0o700)
			m := &state.Manifest{Project: project, Root: repo, Branch: "main"}
			if err := state.WritePrivate(filepath.Join(e.newDir, project, "manifest.json"), m); err != nil {
				t.Fatal(err)
			}
			e.post("/api/actions", map[string]string{"action": "up", "project": project}, reqOpts{})
			got := e.started()
			if ran := len(got) == 1 && got[0] == repo; ran != (mode == "hybrid") {
				t.Fatalf("mode %s: StartEnv calls %v", mode, got)
			}
			if mode == "hybrid" { // another branch checked out: refused, nothing started
				gitIn(t, repo, "checkout", "-q", "-b", "other")
				e.post("/api/actions", map[string]string{"action": "up", "project": project}, reqOpts{})
				if n := len(e.started()); n != 1 {
					t.Fatalf("started another branch: %v", e.started())
				}
			}
		})
	}
}

// hostExecPlan records a container-mode plan whose only host-run step is a
// host exec migrate (the database-only stack).
func hostExecPlan(t *testing.T, repo string) knowledge.Plan {
	t.Helper()
	p := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Once: knowledge.OnceSchema, Cmd: "bun run db:migrate", Dir: "apps/api"},
	}}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: p}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return p
}

// The card's Start of a container-mode plan with a host migrate goes through
// StartEnv (trust check, run record), not a plain compose up.
func TestCardStartWithHostExecRunsThePlan(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hostExecPlan(t, repo)
	project := "sf-card-hostexec"
	os.MkdirAll(filepath.Join(e.newDir, project), 0o700)
	m := &state.Manifest{Project: project, Root: repo, Branch: "main"}
	if err := state.WritePrivate(filepath.Join(e.newDir, project, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	e.post("/api/actions", map[string]string{"action": "up", "project": project}, reqOpts{})
	if got := e.started(); len(got) != 1 || got[0] != repo {
		t.Fatalf("StartEnv calls %v", got)
	}
}

func TestPlanRowShowsHostExecStepWithTrust(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	p := hostExecPlan(t, repo)
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	for _, want := range []string{`data-step="migrate"`, "bun run db:migrate  on host", "untrusted", "Trust on this machine"} {
		if !strings.Contains(frag, want) {
			t.Errorf("missing %q in:\n%s", want, frag)
		}
	}
	if _, err := lifecycle.TrustHere(e.newDir, repo, p); err != nil {
		t.Fatal(err)
	}
	if frag := e.get("/fragments/repos").Body.String(); strings.Contains(frag, "untrusted") || !strings.Contains(frag, "trusted") {
		t.Fatalf("trusted chip missing:\n%s", frag)
	}
}

// The dashboard can trust a plan whose only host-run step is a host exec step.
func TestTrustActionAcceptsAPlanWithOnlyAHostExecStep(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	hostExecPlan(t, repo)
	e.get("/api/repos")
	asked := stubConfirm(t, true)
	e.post("/api/run", map[string]string{"command": "stackfork --root '" + repo + "' plan --trust"}, reqOpts{})
	if !strings.Contains(*asked, "migrate [exec]") || !strings.Contains(*asked, "on: host") {
		t.Fatalf("dialog %q", *asked)
	}
}
