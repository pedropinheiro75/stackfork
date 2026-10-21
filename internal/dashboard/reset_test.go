package dashboard_test

import (
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

type resetCall struct {
	checkout string
	current  bool
	fromMain bool
}

// withReset installs a fake preview and a fake reset on the server and
// answers the native dialog with answer; it returns what each saw.
func (e *browserEnv) withReset(t *testing.T, text string, blocked, answer bool) (asked *string, calls *[]resetCall) {
	t.Helper()
	old := dashboard.Confirm
	t.Cleanup(func() { dashboard.Confirm = old })
	asked, calls = new(string), new([]resetCall)
	dashboard.Confirm = func(title, body string) (bool, error) { *asked = body; return answer, nil }
	var mu sync.Mutex
	e.srv.ResetPreview = func(checkout string, fromMain bool) (string, bool, error) {
		if fromMain {
			return text + "\nUses the main checkout's recipe", blocked, nil
		}
		return text, blocked, nil
	}
	e.srv.ResetEnv = func(out, _ io.Writer, checkout string, current, fromMain bool, _ func(runs.Event)) error {
		mu.Lock()
		*calls = append(*calls, resetCall{checkout, current, fromMain})
		mu.Unlock()
		io.WriteString(out, "rebuilding it\n")
		return nil
	}
	return
}

func TestResetAsksNamingTheVolumesThenStreams(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	asked, calls := e.withReset(t, "Reset sf-x?\nvolume sf-x_db-data", false, true)
	rec := e.post("/api/actions", map[string]string{"action": "reset", "repo": repo, "path": repo}, reqOpts{})
	lines, final := ndjson(t, rec.Body.String())
	if rec.Code != 200 || final["ok"] != true || len(lines) != 1 || lines[0] != "rebuilding it" {
		t.Fatalf("%d lines=%v final=%v", rec.Code, lines, final)
	}
	if !strings.Contains(*asked, "sf-x_db-data") {
		t.Fatalf("dialog: %q", *asked)
	}
	if len(*calls) != 1 || (*calls)[0] != (resetCall{repo, false, false}) {
		t.Fatalf("%v", *calls)
	}
}

func TestResetCancelledRunsNothing(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	_, calls := e.withReset(t, "Reset?", false, false)
	_, final := ndjson(t, e.post("/api/actions", map[string]string{"action": "reset", "repo": repo, "path": repo}, reqOpts{}).Body.String())
	if final["ok"] != false || !strings.Contains(final["message"].(string), "cancelled") || len(*calls) != 0 {
		t.Fatalf("%v %v", final, *calls)
	}
}

// A blocked sync is part of the dialog text; accepting it continues with the current code.
func TestResetBlockedSyncContinuesWithTheCurrentCode(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	_, calls := e.withReset(t, "Reset?\nuses the current code", true, true)
	e.post("/api/actions", map[string]string{"action": "reset", "repo": repo, "path": repo}, reqOpts{})
	if len(*calls) != 1 || !(*calls)[0].current {
		t.Fatalf("%v", *calls)
	}
}

func TestResetIsGuardedLikeStart(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	e.get("/api/repos")
	_, calls := e.withReset(t, "Reset?", false, true)
	body := map[string]string{"action": "reset", "repo": repo, "path": repo}
	if rec := e.post("/api/actions", body, reqOpts{noToken: true}); rec.Code != 403 {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := e.post("/api/actions", body, reqOpts{origin: "http://evil.example"}); rec.Code != 403 {
		t.Errorf("bad origin: %d", rec.Code)
	}
	if rec := e.post("/api/actions", map[string]string{"action": "reset", "repo": repo, "path": filepath.Join(repo, "elsewhere")}, reqOpts{}); rec.Code != 403 {
		t.Errorf("a path the scan does not know: %d", rec.Code)
	}
	if rec := e.post("/api/actions", map[string]string{"action": "reset", "repo": repo, "branch": "main"}, reqOpts{}); rec.Code != 403 {
		t.Errorf("a branch is not a worktree: %d", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatalf("ran: %v", *calls)
	}
	e.srv.ResetEnv = nil
	if rec := e.post("/api/actions", body, reqOpts{}); rec.Code != 503 {
		t.Errorf("unavailable build: %d", rec.Code)
	}
}

// "Reset environment..." is in the worktree row's menu and the VersionCard's, only where an environment exists.
func TestResetMenuItems(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "wt", filepath.Join(repo, ".worktrees", "wt"))
	writeFixtureInstance(t, e.newDir, "sf-ready-1", repo, "main", "web")
	body := e.get("/fragments/repos").Body.String()
	if n := strings.Count(body, `data-repo-action="reset"`); n != 1 {
		t.Fatalf("want one Reset item (the worktree with an environment), got %d:\n%s", n, body)
	}
	if !strings.Contains(body, `data-path="`+repo+`"`) || !strings.Contains(body, "Reset environment&hellip;") {
		t.Error("the item must name its worktree and be labelled")
	}
	page := e.get("/").Body.String()
	if !strings.Contains(page, `data-repo-action="reset" data-repo="`+repo+`" data-path="`+repo+`"`) {
		t.Errorf("VersionCard menu lacks Reset:\n%s", page)
	}
}
