package dashboard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"adapter .agent-dev/app.compose.yaml", "implicit: starts every service", `chip chip-off`, ">derived<", `data-mode-for="` + repo + `"`} {
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
	e.needsLearnRepo("svc")
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	plan := planBlock(t, frag)
	if !strings.Contains(plan, "data-plan-refusal") || !strings.Contains(plan, "callout-attention") || !strings.Contains(plan, "stackfork only starts projects whose startup was learned") {
		t.Errorf("refusal callout missing:\n%s", plan)
	}
	if strings.Contains(plan, "plan-row") {
		t.Errorf("a refusal lists no steps:\n%s", plan)
	}
}

func TestReposDerivedPlanIsCachedUntilAnEvent(t *testing.T) {
	e := newBrowserEnv(t)
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
