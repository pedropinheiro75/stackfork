package dashboard_test

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

type liveFake struct {
	cleaned, kept []string
	cleanErr      error
	textErr       error
	settingTexts  []string
}

// withLiveCleanup installs fakes on the server and answers the native dialog
// with answer (or the error when dialogErr is set); asked collects what the
// dialog said.
func (e *browserEnv) withLiveCleanup(t *testing.T, answer bool, dialogErr error) (f *liveFake, asked *[]string) {
	t.Helper()
	old := dashboard.Confirm
	t.Cleanup(func() { dashboard.Confirm = old })
	asked = new([]string)
	dashboard.Confirm = func(title, body string) (bool, error) {
		*asked = append(*asked, body)
		return answer, dialogErr
	}
	f = &liveFake{}
	e.srv.LiveCleanup = &dashboard.LiveCleanup{
		CleanText: func(main, path string) (string, error) {
			return "Clean up feature-login?\nThe instance acme-web-feature-login is removed with these volumes:\n  acme-web-feature-login_db-data", f.textErr
		},
		Clean: func(out io.Writer, main, path string, locked func(string) bool) error {
			f.cleaned = append(f.cleaned, path)
			return f.cleanErr
		},
		Keep: func(main, path string) error { f.kept = append(f.kept, path); return nil },
		SettingText: func(main string, cl config.Cleanup) (string, error) {
			s := "Worktrees of merged branches and their databases are deleted after " + string(rune('0'+cl.GraceHours/10)) + string(rune('0'+cl.GraceHours%10)) + " hours without asking.\n  acme-web-feature-login_db-data"
			f.settingTexts = append(f.settingTexts, s)
			return s, nil
		},
	}
	return f, asked
}

// liveWorktree is a scanned repo with a linked worktree on feature-login.
func (e *browserEnv) liveWorktree(t *testing.T) (repo, wt string) {
	t.Helper()
	repo = e.readyRepo("acme-web")
	wt = filepath.Join(repo, ".worktrees", "feature-login")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "feature-login", wt, "main")
	wt, _ = filepath.EvalSymlinks(wt)
	e.get("/api/repos")
	return repo, wt
}

func TestLiveCleanCancelledDeletesNothing(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, asked := e.withLiveCleanup(t, false, nil)
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "clean", "path": wt}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cancelled; nothing was deleted") || len(f.cleaned) != 0 || len(*asked) != 1 {
		t.Fatalf("%d %s cleaned=%v asked=%d", rec.Code, rec.Body.String(), f.cleaned, len(*asked))
	}
}

func TestLiveCleanApprovedNamesTheVolumesAndRuns(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, asked := e.withLiveCleanup(t, true, nil)
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "clean", "path": wt}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), "cleaned up feature-login") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(*asked) != 1 || !strings.Contains((*asked)[0], "acme-web-feature-login_db-data") {
		t.Fatalf("the dialog must name the volume: %v", *asked)
	}
	if len(f.cleaned) != 1 || f.cleaned[0] != wt {
		t.Fatalf("cleaned: %v", f.cleaned)
	}
}

func TestLiveCleanWithoutADialogDeletesNothingAndShowsTheCommand(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, _ := e.withLiveCleanup(t, false, errors.New("no confirmation dialog on this system; run it in a terminal"))
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "clean", "path": wt}, reqOpts{})
	if !strings.Contains(rec.Body.String(), "stackfork live clean "+repo+" feature-login --yes") || len(f.cleaned) != 0 || strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("%d %s cleaned=%v", rec.Code, rec.Body.String(), f.cleaned)
	}
}

func TestLiveCleanOfAnIneligibleWorktreeAsksNothing(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, asked := e.withLiveCleanup(t, true, nil)
	f.textErr = errors.New("feature-login: PR merged, but this worktree has uncommitted changes")
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "clean", "path": wt}, reqOpts{})
	if !strings.Contains(rec.Body.String(), "uncommitted changes") || len(*asked) != 0 || len(f.cleaned) != 0 {
		t.Fatalf("%s asked=%d cleaned=%v", rec.Body.String(), len(*asked), f.cleaned)
	}
}

func TestLiveCleanFailureIsReported(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, _ := e.withLiveCleanup(t, true, nil)
	f.cleanErr = errors.New("git refused")
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "clean", "path": wt}, reqOpts{})
	if strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), "failed: git refused") {
		t.Fatalf("%s", rec.Body.String())
	}
}

func TestLiveAPIBoundary(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, asked := e.withLiveCleanup(t, true, nil)
	body := map[string]string{"repo": repo, "op": "clean", "path": wt}
	for name, c := range map[string]struct {
		body map[string]string
		o    reqOpts
		code int
	}{
		"no token":          {body, reqOpts{noToken: true}, 403},
		"foreign origin":    {body, reqOpts{origin: "http://evil.example"}, 403},
		"unknown repo":      {map[string]string{"repo": "/tmp/nowhere", "op": "clean", "path": wt}, reqOpts{}, 403},
		"the main checkout": {map[string]string{"repo": repo, "op": "clean", "path": repo}, reqOpts{}, 403},
		"not a worktree":    {map[string]string{"repo": repo, "op": "clean", "path": "/tmp"}, reqOpts{}, 403},
		"no path":           {map[string]string{"repo": repo, "op": "clean"}, reqOpts{}, 403},
		"unknown op":        {map[string]string{"repo": repo, "op": "obliterate", "path": wt}, reqOpts{}, 400},
		"keep on main":      {map[string]string{"repo": repo, "op": "keep", "path": repo}, reqOpts{}, 403},
	} {
		if rec := e.post("/api/live", c.body, c.o); rec.Code != c.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if len(*asked) != 0 || len(f.cleaned) != 0 || len(f.kept) != 0 {
		t.Fatalf("a refused request acted: asked=%v cleaned=%v kept=%v", *asked, f.cleaned, f.kept)
	}
	e.srv.LiveCleanup = nil
	if rec := e.post("/api/live", body, reqOpts{}); rec.Code != 503 {
		t.Errorf("no cleanup support: %d", rec.Code)
	}
}

func TestLiveKeepNeedsNoDialog(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, asked := e.withLiveCleanup(t, false, nil)
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "keep", "path": wt}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) || len(f.kept) != 1 || f.kept[0] != wt || len(*asked) != 0 {
		t.Fatalf("%d %s kept=%v asked=%v", rec.Code, rec.Body.String(), f.kept, *asked)
	}
}

func cleanupOf(t *testing.T, repo string) config.Cleanup {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.CleanupFor(repo)
}

func TestLiveCleanupAutoAsksAndWritesOnlyAfterApproval(t *testing.T) {
	e := newBrowserEnv(t)
	repo, _ := e.liveWorktree(t)
	before := cleanupOf(t, repo)
	post := func(m map[string]interface{}) string {
		m["repo"], m["op"] = repo, "cleanup"
		return e.post("/api/live", m, reqOpts{}).Body.String()
	}

	// cancelled: nothing written
	_, asked := e.withLiveCleanup(t, false, nil)
	if out := post(map[string]interface{}{"mode": "auto", "grace_hours": 12}); !strings.Contains(out, "cancelled; nothing was changed") || len(*asked) != 1 ||
		!strings.Contains((*asked)[0], "acme-web-feature-login_db-data") || cleanupOf(t, repo) != before {
		t.Fatalf("cancelled: %s asked=%v cfg=%+v", out, *asked, cleanupOf(t, repo))
	}
	// no dialog: nothing written, the command is shown
	e.withLiveCleanup(t, false, errors.New("no confirmation dialog on this system"))
	if out := post(map[string]interface{}{"mode": "auto", "grace_hours": 12}); !strings.Contains(out, "stackfork live cleanup auto "+repo+" --grace 12") || cleanupOf(t, repo) != before {
		t.Fatalf("no dialog: %s cfg=%+v", out, cleanupOf(t, repo))
	}
	// approved: written
	e.withLiveCleanup(t, true, nil)
	if out := post(map[string]interface{}{"mode": "auto", "grace_hours": 12}); !strings.Contains(out, `"ok":true`) {
		t.Fatalf("approved: %s", out)
	}
	if got := cleanupOf(t, repo); got.Mode != "auto" || got.GraceHours != 12 || got.DeleteBranch {
		t.Fatalf("written: %+v", got)
	}
}

func TestLiveCleanupBranchDeletionAsksSuggestAndOffDoNot(t *testing.T) {
	e := newBrowserEnv(t)
	repo, _ := e.liveWorktree(t)
	_, asked := e.withLiveCleanup(t, true, nil)
	post := func(m map[string]interface{}) (string, int) {
		m["repo"], m["op"] = repo, "cleanup"
		rec := e.post("/api/live", m, reqOpts{})
		return rec.Body.String(), rec.Code
	}
	if out, _ := post(map[string]interface{}{"mode": "suggest", "delete_branch": true}); !strings.Contains(out, `"ok":true`) || len(*asked) != 1 || !cleanupOf(t, repo).DeleteBranch {
		t.Fatalf("delete_branch: %s asked=%d", out, len(*asked))
	}
	*asked = nil
	if out, _ := post(map[string]interface{}{"mode": "suggest"}); !strings.Contains(out, `"ok":true`) || len(*asked) != 0 || !cleanupOf(t, repo).DeleteBranch {
		t.Fatalf("suggest keeps the stored setting and asks nothing: %s asked=%d", out, len(*asked))
	}
	if out, _ := post(map[string]interface{}{"mode": "suggest", "delete_branch": false}); !strings.Contains(out, `"ok":true`) || len(*asked) != 0 || cleanupOf(t, repo).DeleteBranch {
		t.Fatalf("keep-branch asks nothing: %s", out)
	}
	if out, _ := post(map[string]interface{}{"mode": "off"}); !strings.Contains(out, `"ok":true`) || len(*asked) != 0 || cleanupOf(t, repo).Mode != "off" {
		t.Fatalf("off asks nothing: %s", out)
	}
	if _, code := post(map[string]interface{}{"mode": "sometimes"}); code != 400 {
		t.Errorf("bad mode: %d", code)
	}
	if _, code := post(map[string]interface{}{"mode": "suggest", "grace_hours": 0}); code != 400 {
		t.Errorf("bad grace: %d", code)
	}
	if _, code := post(map[string]interface{}{"mode": "suggest", "grace_hours": 721}); code != 400 {
		t.Errorf("bad grace: %d", code)
	}
	if cleanupOf(t, repo).GraceHours != 24 {
		t.Errorf("a refused grace changed the setting: %+v", cleanupOf(t, repo))
	}
}
