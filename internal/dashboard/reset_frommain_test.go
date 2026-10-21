package dashboard_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// from_main reaches both the preview (the text the dialog shows) and the run.
func TestResetFromMainReachesPreviewAndRun(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	asked, calls := e.withReset(t, "Reset sf-x?", false, true)
	rec := e.post("/api/actions", map[string]any{"action": "reset", "repo": repo, "path": repo, "from_main": true}, reqOpts{})
	_, final := ndjson(t, rec.Body.String())
	if rec.Code != 200 || final["ok"] != true {
		t.Fatalf("%d %v", rec.Code, final)
	}
	if !strings.Contains(*asked, "Uses the main checkout's recipe") {
		t.Fatalf("the dialog lacks the from-main text: %q", *asked)
	}
	if len(*calls) != 1 || (*calls)[0] != (resetCall{repo, false, true}) {
		t.Fatalf("%v", *calls)
	}
}

func TestResetFromMainCancelStartsNoRun(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	_, calls := e.withReset(t, "Reset?", false, false)
	_, final := ndjson(t, e.post("/api/actions", map[string]any{"action": "reset", "repo": repo, "path": repo, "from_main": true}, reqOpts{}).Body.String())
	if final["ok"] != false || !strings.Contains(final["message"].(string), "cancelled") || len(*calls) != 0 {
		t.Fatalf("%v %v", final, *calls)
	}
}

// from_main belongs to reset only, and needs a worktree the scan knows.
func TestResetFromMainValidation(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	_, calls := e.withReset(t, "Reset?", false, true)
	if rec := e.post("/api/actions", map[string]any{"action": "start", "repo": repo, "path": repo, "from_main": true}, reqOpts{}); rec.Code != 400 {
		t.Errorf("from_main with start: %d", rec.Code)
	}
	if rec := e.post("/api/actions", map[string]any{"action": "reset", "repo": repo, "branch": "main", "from_main": true}, reqOpts{}); rec.Code != 403 {
		t.Errorf("a branch is not a worktree: %d", rec.Code)
	}
	if rec := e.post("/api/actions", map[string]any{"action": "reset", "repo": repo, "path": repo + "/elsewhere", "from_main": true}, reqOpts{}); rec.Code != 403 {
		t.Errorf("a path the scan does not know: %d", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatalf("ran: %v", *calls)
	}
}

// "Reset from main checkout…" is offered for a linked worktree with an
// environment whose main checkout has a prepared instance, in the Repos
// worktree row and in the VersionCard menu, and nowhere else.
func TestResetFromMainMenuItems(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	wt := filepath.Join(repo, ".worktrees", "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "wt", wt)
	writeFixtureInstance(t, e.newDir, "sf-ready-wt-1", wt, "wt", "web")
	writeFixtureInstance(t, e.newDir, "sf-ready-1", repo, "main", "web")
	body, page := e.get("/fragments/repos").Body.String(), e.get("/").Body.String()
	if n := strings.Count(body, `data-from-main="1"`); n != 1 {
		t.Fatalf("want one item in the Repos panel (the linked worktree), got %d:\n%s", n, body)
	}
	if !strings.Contains(body, `data-repo-action="reset" data-from-main="1" data-repo="`+repo+`" data-path="`+wt+`"`) || !strings.Contains(body, "Reset from main checkout&hellip;") {
		t.Error("the Repos item must name its worktree and be labelled")
	}
	if n := strings.Count(page, `data-from-main="1"`); n != 1 {
		t.Fatalf("want one item on the VersionCard of the linked worktree (not the main checkout's), got %d", n)
	}
	if !strings.Contains(page, `data-repo-action="reset" data-from-main="1" data-repo="`+repo+`" data-path="`+wt+`"`) {
		t.Error("the VersionCard item must name its worktree")
	}
}

func TestResetFromMainMenuItemsNeedAMainInstance(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	wt := filepath.Join(repo, ".worktrees", "wt")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "wt", wt)
	writeFixtureInstance(t, e.newDir, "sf-ready-wt-1", wt, "wt", "web")
	if body, page := e.get("/fragments/repos").Body.String(), e.get("/").Body.String(); strings.Contains(body, "Reset from main checkout") || strings.Contains(page, "Reset from main checkout") {
		t.Fatal("offered while the main checkout has no prepared instance")
	}
}
