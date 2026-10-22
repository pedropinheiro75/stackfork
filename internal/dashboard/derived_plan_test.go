package dashboard_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const derivedLabel = "derived — not recorded; run learn to record it"

// planBlock is the plan section of the first repo in the fragment.
func planBlock(t *testing.T, frag string) string {
	t.Helper()
	i := strings.Index(frag, `class="plan"`)
	if i < 0 {
		t.Fatalf("no plan section:\n%s", frag)
	}
	j := strings.Index(frag[i:], `class="secrets"`)
	if j < 0 {
		j = len(frag) - i
	}
	return frag[i : i+j]
}

func TestReposShowsTheDerivedPlan(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	plan := planBlock(t, frag)
	for _, want := range []string{derivedLabel, `data-plan="derived"`, `data-step="prepare"`, `data-step="up"`, "Learn with your agent",
		"adapter .agent-dev/app.compose.yaml", "implicit: starts every service", `chip chip-off`, ">derived<", `data-mode-label`} {
		if !strings.Contains(plan, want) {
			t.Errorf("missing %q in:\n%s", want, plan)
		}
	}
	if strings.Contains(plan, "btn-primary") {
		t.Error("the plan list has no primary button")
	}
	if rec := e.post("/api/actions", map[string]string{"action": "recipe-seed", "repo": repo}, reqOpts{}); rec.Code != 400 {
		t.Errorf("the recipe action is gone: %d", rec.Code)
	}
	for _, gone := range []string{"Starts with", "data-recipe", "recipe/copy", "stackfork learn record recipe"} {
		if strings.Contains(frag, gone) {
			t.Errorf("the recipe line is gone, found %q", gone)
		}
	}
}

func TestReposDerivedPlanEscapesHostileSources(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"db:migrate":"m","db:seed\"><img src=x onerror=alert(1)>":"s"}}`), 0o644)
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	if strings.Contains(frag, "<img src=x") || !strings.Contains(frag, "&lt;img src=x") && !strings.Contains(frag, "&#34;&gt;&lt;img src=x") {
		t.Fatalf("hostile source not escaped:\n%s", frag)
	}
	if !strings.Contains(frag, "npm run db:migrate") || !strings.Contains(frag, "auto-detected: ") {
		t.Errorf("the detected seed is listed:\n%s", planBlock(t, frag))
	}
}

func TestReposDerivedPlanShowsARefusal(t *testing.T) {
	e := newBrowserEnv(t)
	// ready (verified knowledge) but no recipe anywhere
	repo := scratchRepoAt(t, filepath.Join(e.root, "norecipe"))
	if err := (&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	plan := planBlock(t, frag)
	if !strings.Contains(plan, "data-plan-refusal") || !strings.Contains(plan, "callout-attention") || !strings.Contains(plan, "no recorded prepare recipe") {
		t.Errorf("refusal callout missing:\n%s", plan)
	}
	if strings.Contains(plan, "plan-row") {
		t.Errorf("a refusal lists no steps:\n%s", plan)
	}
}

// A repo that is not set up shows only the setup callout, and nothing is derived for it.
func TestReposNeedsLearnRepoIsNotDerived(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.needsLearnRepo("svc")
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	if !strings.Contains(frag, "data-setup-callout") || strings.Contains(frag, `data-plan=`) || strings.Contains(frag, "A Start would be refused") {
		t.Errorf("only the setup callout expected:\n%s", frag)
	}
	if n := e.derived(repo); n != 0 {
		t.Errorf("derived %d times for a repo that is not set up", n)
	}
}

func TestReposDerivedPlanHasNoModeSelect(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	cfg, _ := config.Load()
	cfg.SetRepoMode(repo, "hybrid")
	cfg.Save()
	e.get("/api/repos")
	plan := planBlock(t, e.get("/fragments/repos").Body.String())
	if strings.Contains(plan, `<option value="hybrid"`) {
		t.Errorf("a select offers hybrid:\n%s", plan)
	}
	if strings.Contains(plan, "<select") && !strings.Contains(plan, "not supported by this plan") {
		t.Errorf("one mode: no select:\n%s", plan)
	}
	if !strings.Contains(plan, "<select") && !strings.Contains(plan, "data-mode-label") {
		t.Errorf("mode label missing:\n%s", plan)
	}
}

func TestReposDerivedStepsShowTheirSourceOnHover(t *testing.T) {
	e := newBrowserEnv(t)
	e.readyRepo("svc")
	e.get("/api/repos")
	plan := planBlock(t, e.get("/fragments/repos").Body.String())
	if !strings.Contains(plan, `title="adapter .agent-dev/app.compose.yaml"`) || !strings.Contains(plan, `title="implicit: starts every service"`) {
		t.Errorf("source titles missing:\n%s", plan)
	}
}

func TestReposDerivedPlanIsCachedUntilAnEvent(t *testing.T) {
	e := newBrowserEnv(t)
	e.srv.RescanAfter = time.Hour // only an event may drop the cache, never the clock
	repo := e.readyRepo("svc")
	e.get("/api/repos")
	for i := 0; i < 3; i++ {
		e.get("/fragments/repos")
	}
	if n := e.derived(repo); n != 1 {
		t.Fatalf("derived %d times in three renders", n)
	}
	rec := e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	e.srv.Wait()
	e.get("/fragments/repos")
	if n := e.derived(repo); n != 2 {
		t.Fatalf("derived %d times after a Start, want 2", n)
	}
}

func TestReposWithoutTheHookShowsNoPlanAndNoRecipeLine(t *testing.T) {
	e := newBrowserEnv(t)
	e.readyRepo("svc")
	e.srv.DerivePlan = nil
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	if strings.Contains(frag, `class="plan"`) || strings.Contains(frag, "Starts with") {
		t.Errorf("no hook, no derived plan:\n%s", frag)
	}
}

// Knowledge verified in a linked worktree serves the whole repo: the panel
// shows its recorded plan and where it lives, never "run learn".
func TestReposShowsKnowledgeVerifiedInAWorktree(t *testing.T) {
	e := newBrowserEnv(t)
	repo := scratchRepoAt(t, filepath.Join(e.root, "svc"))
	os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(fixtureCompose), 0o644)
	wt := filepath.Join(repo, ".worktrees", "chore-218")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "chore-218", wt).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified,
		Verification: &knowledge.Verification{Date: "2026-05-01", Confirmation: "works"},
		Plan:         knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}}}}
	if err := k.Save(filepath.Join(wt, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	e.get("/api/repos")
	plan := planBlock(t, e.get("/fragments/repos").Body.String())
	if strings.Contains(plan, derivedLabel) || !strings.Contains(plan, `data-step="up"`) || !strings.Contains(plan, "from worktree chore-218 (verified)") {
		t.Errorf("plan:\n%s", plan)
	}
}
