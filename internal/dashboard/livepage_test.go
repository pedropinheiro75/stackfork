package dashboard_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/live"
)

// liveOpsFake is a LiveOps whose snapshot a test sets and whose calls it reads.
type liveOpsFake struct {
	st                               live.Status
	prepared, enabled, resumed       []string
	prepareErr, enableErr, resumeErr error
}

func (e *browserEnv) withLiveOps(t *testing.T, answer bool, dialogErr error) (f *liveOpsFake, asked *[]string) {
	t.Helper()
	old := dashboard.Confirm
	t.Cleanup(func() { dashboard.Confirm = old })
	asked = new([]string)
	dashboard.Confirm = func(title, body string) (bool, error) {
		*asked = append(*asked, body)
		return answer, dialogErr
	}
	f = &liveOpsFake{}
	e.srv.Live = &dashboard.LiveOps{
		Status:  func() live.Status { return f.st },
		Prepare: func(main string) error { f.prepared = append(f.prepared, main); return f.prepareErr },
		Enable: func(main string) error {
			f.enabled = append(f.enabled, main)
			if f.enableErr != nil {
				return f.enableErr
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := cfg.SetLive(main); err != nil {
				return err
			}
			return cfg.Save()
		},
		Resume: func(main string) error { f.resumed = append(f.resumed, main); return f.resumeErr },
	}
	return f, asked
}

func liveOn(t *testing.T, repo string) bool {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.LiveFor(repo)
}

func TestLiveOnAsksWithTheConsentTextAndWritesOnApproval(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, asked := e.withLiveOps(t, true, nil)
	e.get("/api/repos")
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "on"}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(*asked) != 1 || !strings.Contains((*asked)[0], "acme-web") || !strings.Contains((*asked)[0], "fetched, fast-forwarded and built on this machine") {
		t.Fatalf("the dialog must name the repo and say what happens: %q", *asked)
	}
	if len(f.prepared) != 1 || len(f.enabled) != 1 || !liveOn(t, repo) {
		t.Fatalf("prepared=%v enabled=%v on=%v", f.prepared, f.enabled, liveOn(t, repo))
	}
}

func TestLiveOnCancelledWritesNothing(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, asked := e.withLiveOps(t, false, nil)
	e.get("/api/repos")
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "on"}, reqOpts{})
	if !strings.Contains(rec.Body.String(), "cancelled") || strings.Contains(rec.Body.String(), `"ok":true`) || len(*asked) != 1 || len(f.enabled) != 0 || liveOn(t, repo) {
		t.Fatalf("%s asked=%d enabled=%v on=%v", rec.Body.String(), len(*asked), f.enabled, liveOn(t, repo))
	}
}

func TestLiveOnWithoutADialogShowsTheCommandAndWritesNothing(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, _ := e.withLiveOps(t, false, errors.New("no confirmation dialog on this system"))
	e.get("/api/repos")
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "on"}, reqOpts{})
	var got struct {
		OK      bool
		Command string
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.OK || got.Command != "stackfork live on '"+repo+"'" || len(f.enabled) != 0 || liveOn(t, repo) {
		t.Fatalf("%s enabled=%v", rec.Body.String(), f.enabled)
	}
}

func TestLiveOnRefusedByTheChecksOpensNoDialog(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, asked := e.withLiveOps(t, true, nil)
	f.prepareErr = errors.New("the plan needs trust on this machine before live mode can run it")
	e.get("/api/repos")
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "on"}, reqOpts{})
	if !strings.Contains(rec.Body.String(), "needs trust") || len(*asked) != 0 || len(f.enabled) != 0 || liveOn(t, repo) {
		t.Fatalf("%s asked=%d", rec.Body.String(), len(*asked))
	}
}

func TestLiveOffAndResumeNeedNoDialog(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, asked := e.withLiveOps(t, false, errors.New("no dialog"))
	cfg, _ := config.Load()
	cfg.SetLive(repo)
	cfg.Save()
	e.get("/api/repos")
	if rec := e.post("/api/live", map[string]string{"repo": repo, "op": "resume"}, reqOpts{}); !strings.Contains(rec.Body.String(), `"ok":true`) || len(f.resumed) != 1 {
		t.Fatalf("resume: %s", rec.Body.String())
	}
	if rec := e.post("/api/live", map[string]string{"repo": repo, "op": "off"}, reqOpts{}); !strings.Contains(rec.Body.String(), `"ok":true`) || liveOn(t, repo) {
		t.Fatalf("off: %s on=%v", rec.Body.String(), liveOn(t, repo))
	}
	if len(*asked) != 0 {
		t.Fatalf("off and resume opened a dialog: %v", *asked)
	}
}

func TestLiveResumeRefusedWhileTheMessageNamesTheSteps(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, _ := e.withLiveOps(t, true, nil)
	f.resumeErr = errors.New("Plan changed; host steps need trust:\n+ host: npm run dev")
	e.get("/api/repos")
	rec := e.post("/api/live", map[string]string{"repo": repo, "op": "resume"}, reqOpts{})
	if strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), "host steps need trust") {
		t.Fatalf("%s", rec.Body.String())
	}
}

func TestLiveSwitchBoundary(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, asked := e.withLiveOps(t, true, nil)
	e.get("/api/repos")
	for name, c := range map[string]struct {
		body map[string]string
		o    reqOpts
		code int
	}{
		"no token":       {map[string]string{"repo": repo, "op": "on"}, reqOpts{noToken: true}, 403},
		"foreign origin": {map[string]string{"repo": repo, "op": "on"}, reqOpts{origin: "http://evil.example"}, 403},
		"unknown repo":   {map[string]string{"repo": "/tmp/nowhere", "op": "on"}, reqOpts{}, 403},
		"unknown op":     {map[string]string{"repo": repo, "op": "enable"}, reqOpts{}, 400},
	} {
		if rec := e.post("/api/live", c.body, c.o); rec.Code != c.code {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body.String(), c.code)
		}
	}
	if len(*asked) != 0 || len(f.enabled) != 0 || liveOn(t, repo) {
		t.Fatalf("a refused call must open no dialog and write nothing: asked=%v", *asked)
	}
}

func TestLiveSwitchWithoutLiveOpsAnswers503(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	e.get("/api/repos")
	if rec := e.post("/api/live", map[string]string{"repo": repo, "op": "on"}, reqOpts{}); rec.Code != 503 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// reposLive is /api/repos' live map.
func (e *browserEnv) reposLive(t *testing.T) map[string]map[string]any {
	t.Helper()
	var doc struct {
		Live map[string]map[string]any `json:"live"`
	}
	if err := json.Unmarshal(e.get("/api/repos").Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Live
}

func TestReposAPICarriesTheLiveState(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, _ := e.withLiveOps(t, true, nil)
	if got := e.reposLive(t)[repo]; got["enabled"] != false || got["state"] != "off" {
		t.Fatalf("off: %v", got)
	}
	cfg, _ := config.Load()
	cfg.SetLive(repo)
	cfg.Save()
	f.st = live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StatePaused, Reason: "run 23 failed at step rebuild",
		Pause:   &live.Pause{Kind: live.PauseFailed, Reason: "run 23 failed at step rebuild", Run: 23, Step: "rebuild"},
		Targets: []live.TargetStatus{{Root: repo, Pending: []live.Change{{Path: "bun.lock", Category: "build"}}}}}}}
	got := e.reposLive(t)[repo]
	if got["enabled"] != true || got["state"] != "paused" || got["reason"] != "run 23 failed at step rebuild" || got["run"] != float64(23) {
		t.Fatalf("paused: %v", got)
	}
	if p, _ := got["pending"].(map[string]any); p["build"] != float64(1) {
		t.Fatalf("pending: %v", got["pending"])
	}
}

func (e *browserEnv) fragment() string { return e.get("/fragments/repos").Body.String() }

func TestReposFragmentLiveStates(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, _ := e.withLiveOps(t, true, nil)
	cfg, _ := config.Load()

	// off: switch unchecked, Off chip, no Live tag
	html := e.fragment()
	for _, w := range []string{`role="switch"`, `aria-checked="false"`, `data-live-op="on"`, "Off</span>"} {
		if !strings.Contains(html, w) {
			t.Errorf("off: missing %q", w)
		}
	}
	if strings.Contains(html, `class="live-tag`) {
		t.Error("off: a Live tag")
	}

	cfg.SetLive(repo)
	cfg.Save()
	for name, c := range map[string]struct {
		st   live.Status
		want []string
		not  []string
	}{
		"on": {live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StateOK}}},
			[]string{`aria-checked="true"`, `data-live-op="off"`, "chip chip-ok", "Live</span>", `class="live-tag`}, []string{"Resume"}},
		"busy": {live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StateBusy}}},
			[]string{"chip chip-busy", "Reconciling…"}, []string{"Resume"}},
		"paused": {live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StatePaused, Pause: &live.Pause{Kind: live.PauseFailed, Reason: "run 23 failed at step rebuild", Run: 23}}}},
			[]string{"chip chip-attention", "Paused", "run 23 failed at step rebuild", `data-live-op="resume"`, "live-tag live-tag-attention"}, []string{"Trust on this machine"}},
		"trust": {live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StatePaused, Pause: &live.Pause{Kind: live.PauseTrust, Reason: "plan needs trust", Diff: []string{"+ host: npm run dev"}}}}},
			[]string{"Plan changed; host steps need trust", `data-live-op="resume"`, "Trust on this machine", "plan --trust"}, nil},
		"elsewhere": {live.Status{Elsewhere: true},
			[]string{"In another dashboard", "disabled title=\"Another dashboard"}, []string{"Resume"}},
	} {
		f.st = c.st
		html := e.fragment()
		for _, w := range c.want {
			if !strings.Contains(html, w) {
				t.Errorf("%s: missing %q", name, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(html, n) {
				t.Errorf("%s: has %q", name, n)
			}
		}
	}
}

func TestReposFragmentWithoutLiveOpsHasNoLiveControls(t *testing.T) {
	e := newBrowserEnv(t)
	e.readyRepo("acme-web")
	html := e.fragment()
	if strings.Contains(html, "data-live-op") || strings.Contains(html, "live-tag") {
		t.Fatal("live controls without LiveOps")
	}
}

func TestReposFragmentLiveEscapesHostileNames(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo(`x"><img src=x onerror=alert(1)>`)
	f, _ := e.withLiveOps(t, true, nil)
	cfg, _ := config.Load()
	cfg.SetLive(repo)
	cfg.Save()
	f.st = live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StatePaused, Pause: &live.Pause{Kind: live.PauseFailed, Reason: `run 1 failed at step <b onclick=x>`}}}}
	html := e.fragment()
	if strings.Contains(html, "<img src=x") || strings.Contains(html, "<b onclick") {
		t.Fatalf("unescaped markup in the live block")
	}
	if !strings.Contains(html, "&lt;b onclick=x&gt;") {
		t.Fatal("the reason is not shown escaped")
	}
}

func TestReposFragmentWorktreeLiveWarnings(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, _ := e.withLiveOps(t, true, nil)
	cfg, _ := config.Load()
	cfg.SetLive(repo)
	cfg.Save()
	f.st = live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StateOK, Targets: []live.TargetStatus{{Root: wt, Warnings: []string{
		"warning: feature-login is 2 commits behind origin/feature-login; stackfork did not update it because it has 1 local commit. Update it soon: git -C " + wt + " pull --rebase"}}}}}}
	html := e.fragment()
	for _, w := range []string{"Behind, not updated", "wt-note", "pull --rebase", `data-copy="git -C ` + wt + ` pull --rebase"`} {
		if !strings.Contains(html, w) {
			t.Errorf("blocked fast-forward: missing %q", w)
		}
	}
	f.st.Repos[0].Targets[0].Warnings = []string{"branch switched"}
	if html = e.fragment(); !strings.Contains(html, "Branch switched") || strings.Contains(html, "Behind, not updated") {
		t.Errorf("branch switched: %s", html)
	}
	f.st.Repos[0].Targets[0].Warnings = nil
	if html = e.fragment(); strings.Contains(html, "wt-note") || strings.Contains(html, "Branch switched") {
		t.Error("the notes must clear when the condition clears")
	}
}

func cleanupStatus(repo, wt string, mode string, c live.CandidateStatus) live.Status {
	c.Branch, c.Path = "feature-login", wt
	return live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StateOK, Cleanup: &live.CleanupStatus{
		Mode: mode, GraceHours: 24, Candidates: []live.CandidateStatus{c}}}}}
}

func TestReposFragmentCleanupCallouts(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	f, _ := e.withLiveOps(t, true, nil)
	cfg, _ := config.Load()
	cfg.SetLive(repo)
	cfg.Save()
	soon := time.Now().Add(22*time.Hour + 30*time.Minute)
	for name, c := range map[string]struct {
		st   live.Status
		want []string
		not  []string
	}{
		"suggest": {cleanupStatus(repo, wt, "suggest", live.CandidateStatus{State: "merged", Eligible: true, PR: 42, URL: "https://example.com/acme/web/pull/42"}),
			[]string{"PR merged — clean up?", `data-live-op="clean"`, ">Clean up<", `data-live-op="keep"`, ">Keep<", "#42"}, []string{"Clean up now", "Cleanup in"}},
		"auto": {cleanupStatus(repo, wt, "auto", live.CandidateStatus{State: "merged", Eligible: true, CleanAt: soon}),
			[]string{"Cleanup in 23 h", "Clean up now", `data-live-op="keep"`}, []string{"PR merged — clean up?"}},
		"uncommitted": {cleanupStatus(repo, wt, "suggest", live.CandidateStatus{State: "merged", Warning: "PR merged, but this worktree has uncommitted changes"}),
			[]string{"PR merged, but this worktree has uncommitted changes"}, []string{`data-live-op="clean"`, `data-live-op="keep"`}},
		"closed": {cleanupStatus(repo, wt, "suggest", live.CandidateStatus{State: "closed"}),
			[]string{"PR closed without merge"}, []string{`data-live-op="clean"`}},
		"kept": {cleanupStatus(repo, wt, "suggest", live.CandidateStatus{State: "merged", Eligible: true, Kept: true}),
			nil, []string{"PR merged", "data-live-op=\"clean\""}},
	} {
		f.st = c.st
		html := e.fragment()
		for _, w := range c.want {
			if !strings.Contains(html, w) {
				t.Errorf("%s: missing %q", name, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(html, n) {
				t.Errorf("%s: has %q", name, n)
			}
		}
	}
}

func TestReposFragmentCleanupSelector(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("acme-web")
	f, _ := e.withLiveOps(t, true, nil)
	cfg, _ := config.Load()
	cfg.SetLive(repo)
	cfg.SetCleanup(repo, config.Cleanup{Mode: "auto", GraceHours: 48, DeleteBranch: true})
	cfg.Save()
	f.st = live.Status{Repos: []live.RepoStatus{{Main: repo, State: live.StateOK, Cleanup: &live.CleanupStatus{Mode: "auto", GraceHours: 48, DeleteBranch: true}}}}
	html := e.fragment()
	for _, w := range []string{`data-cleanup-for="` + repo + `"`, `<option value="auto" selected>`, `value="48"`, `data-cleanup-delete`, "checked"} {
		if !strings.Contains(html, w) {
			t.Errorf("selector: missing %q", w)
		}
	}
	// off: no selector
	cfg, _ = config.Load()
	cfg.UnsetLive(repo)
	cfg.Save()
	if html = e.fragment(); strings.Contains(html, "data-cleanup-for") {
		t.Error("the cleanup selector shows with live mode off")
	}
}

func TestInstancesJSONCarriesTheCleanupNoteOfItsWorktree(t *testing.T) {
	e := newBrowserEnv(t)
	repo, wt := e.liveWorktree(t)
	writeFixtureInstance(t, e.newDir, "sf-acme-web-feature-login", wt, "feature-login", "web")
	f, _ := e.withLiveOps(t, true, nil)
	cfg, _ := config.Load()
	cfg.SetLive(repo)
	cfg.Save()
	f.st = cleanupStatus(repo, wt, "suggest", live.CandidateStatus{State: "merged", Eligible: true, Project: "sf-acme-web-feature-login", PR: 42})
	time.Sleep(2100 * time.Millisecond) // the inventory cache (2 s) was filled by the scan before the instance existed
	e.get("/api/repos")                 // the card joins the scan
	var inv struct {
		Instances []struct {
			Project string
			Cleanup *struct{ Repo, Path, Branch, Title, Ask string }
		}
	}
	if err := json.Unmarshal(e.get("/api/instances").Body.Bytes(), &inv); err != nil || len(inv.Instances) != 1 {
		t.Fatalf("%v %s", err, e.get("/api/instances").Body.String())
	}
	c := inv.Instances[0].Cleanup
	if c == nil || c.Repo != repo || c.Path != wt || c.Branch != "feature-login" || c.Title != "PR merged — clean up?" || c.Ask != "suggest" {
		t.Fatalf("cleanup note: %+v", c)
	}
	if !strings.Contains(e.get("/").Body.String(), `data-cleanup="suggest"`) {
		t.Fatal("the first paint lacks the callout")
	}
}
