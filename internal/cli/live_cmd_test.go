package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// liveCmdFx is a git repository, a temp config dir, two temp state dirs and
// no instance (liveTargets returns what a test sets).
type liveCmdFx struct {
	repo, nb, lb string
	targets      []live.Target
}

func newLiveCmdFx(t *testing.T) *liveCmdFx {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	repo, _ := identity.ResolvePath(initRepo(t))
	f := &liveCmdFx{repo: repo, nb: t.TempDir(), lb: t.TempDir()}
	liveTargets = func(string, string, string) ([]live.Target, error) { return f.targets, nil }
	t.Cleanup(func() {
		liveTargets = func(nb, lb, main string) ([]live.Target, error) { return live.Targets(nb, lb, main, Version) }
	})
	return f
}

func (f *liveCmdFx) run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runCLI(t, append([]string{"--root", f.repo, "--state-dir", f.nb, "--legacy-state-dir", f.lb, "live"}, args...)...)
}

func (f *liveCmdFx) holdDashboard(t *testing.T) {
	t.Helper()
	os.MkdirAll(filepath.Join(f.nb, "live"), 0o700)
	lk, err := state.TryLock(live.LockPath(f.nb))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lk.Release() })
}

func TestLiveOnOffWritesConfigAndExplains(t *testing.T) {
	f := newLiveCmdFx(t)
	out, errOut, code := f.run(t, "on")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	want := "live mode on for " + f.repo + "\n" +
		"While the dashboard runs, commits on the upstream of each worktree are fetched, fast-forwarded and built on this machine.\n" +
		"Branch cleanup is on suggest: it always asks first, unless you choose auto.\n" +
		"paused: dashboard not running; start it with stackfork dashboard\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if cfg, _ := config.Load(); !cfg.LiveFor(f.repo) {
		t.Fatal("config.json must hold the key")
	}
	f.holdDashboard(t)
	if out, _, _ := f.run(t, "on"); strings.Contains(out, "dashboard not running") {
		t.Fatalf("a dashboard holds the lock: no note\n%s", out)
	}
	out, _, code = f.run(t, "off")
	if code != 0 || out != "live mode off for "+f.repo+"; a live run in progress finishes\n" {
		t.Fatalf("%d %q", code, out)
	}
	if cfg, _ := config.Load(); cfg.LiveFor(f.repo) || len(cfg.Live) != 0 {
		t.Fatalf("the key must be removed: %+v", cfg.Live)
	}
}

func TestLiveOnClearsAPause(t *testing.T) {
	f := newLiveCmdFx(t)
	live.WritePause(f.nb, f.repo, live.Pause{Kind: live.PauseFailed, Reason: "run 3 failed at step rebuild"})
	f.run(t, "on")
	if p, _ := live.ReadPause(f.nb, f.repo); p != nil {
		t.Fatalf("live on resumes: %+v", p)
	}
}

func TestLiveOnRefusesEnvReferencesAndNotesOnePassword(t *testing.T) {
	f := newLiveCmdFx(t)
	if _, e, c := runCLI(t, "--state-dir", f.nb, "--legacy-state-dir", f.lb, "secret", "map", f.repo, "NPM_TOKEN=env:NPM_TOKEN"); c != 0 {
		t.Fatalf("map: %s", e)
	}
	_, errOut, code := f.run(t, "on")
	if code == 0 || !strings.Contains(errOut, "NPM_TOKEN") || !strings.Contains(errOut, "env:") || !strings.Contains(errOut, "stackfork secret set") {
		t.Fatalf("%d %s", code, errOut)
	}
	if cfg, _ := config.Load(); cfg.LiveFor(f.repo) {
		t.Fatal("a refused on writes nothing")
	}
	runCLI(t, "--state-dir", f.nb, "--legacy-state-dir", f.lb, "secret", "unmap", f.repo, "NPM_TOKEN")
	runCLI(t, "--state-dir", f.nb, "--legacy-state-dir", f.lb, "secret", "map", f.repo, "STRIPE=op://Dev/acme-stripe/test-key")
	out, errOut, code := f.run(t, "on")
	if code != 0 || !strings.Contains(out, "1Password may ask for approval") {
		t.Fatalf("%d %s %s", code, out, errOut)
	}
}

func saveRepoKnowledge(t *testing.T, repo string, mut func(*knowledge.Knowledge)) {
	t.Helper()
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded,
		Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}}}}
	mut(k)
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
}

func TestLiveOnRefusesAPlanThatNeedsTrust(t *testing.T) {
	f := newLiveCmdFx(t)
	saveRepoKnowledge(t, f.repo, func(k *knowledge.Knowledge) {
		k.Plan.Steps = append(k.Plan.Steps, knowledge.Step{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "bun dev"})
	})
	_, errOut, code := f.run(t, "on")
	if code == 0 || !strings.Contains(errOut, "needs trust") || !strings.Contains(errOut, "app") || !strings.Contains(errOut, "stackfork plan --trust") {
		t.Fatalf("%d %s", code, errOut)
	}
	if cfg, _ := config.Load(); cfg.LiveFor(f.repo) {
		t.Fatal("nothing written")
	}
}

func TestLiveOnRefusesBadOverrides(t *testing.T) {
	f := newLiveCmdFx(t)
	saveRepoKnowledge(t, f.repo, func(k *knowledge.Knowledge) {
		k.Live.Watch = []knowledge.LiveWatch{{Path: "web/Dockerfile.dev", Action: "rebuild", Service: "nope"}}
	})
	_, errOut, code := f.run(t, "on")
	if code == 0 || !strings.Contains(errOut, `service "nope"`) {
		t.Fatalf("%d %s", code, errOut)
	}
}

func TestLiveStatusTextAndJSON(t *testing.T) {
	f := newLiveCmdFx(t)
	runsNow = func() time.Time { return time.Date(2026, 10, 20, 9, 5, 0, 0, time.UTC) }
	t.Cleanup(func() { runsNow = time.Now })

	// Nothing on.
	if out, _, code := f.run(t, "status"); code != 0 || out != "live mode is off for every repository; turn it on with stackfork live on [REPO]\n" {
		t.Fatalf("%d %q", code, out)
	}
	// REPO given, off.
	out, _, _ := f.run(t, "status", f.repo)
	if want := filepath.Base(f.repo) + " (" + f.repo + "): off\n"; out != want {
		t.Fatalf("got %q want %q", out, want)
	}

	cfg, _ := config.Load()
	cfg.SetLive(f.repo)
	cfg.Save()
	f.targets = []live.Target{{Main: f.repo, Root: f.repo, Project: "acme-web-feature-login", InstanceDir: filepath.Join(f.nb, "acme-web-feature-login"), Branch: "feature-login", State: "running", NewBase: f.nb}}

	// No dashboard: paused.
	out, _, _ = f.run(t, "status")
	if !strings.Contains(out, ": on, paused: dashboard not running\n  start it with: stackfork dashboard\n  cleanup: suggest\n  acme-web-feature-login (feature-login, running)\n") {
		t.Fatalf("%s", out)
	}
	// A dashboard runs, a failed run paused the repository.
	f.holdDashboard(t)
	live.WritePause(f.nb, f.repo, live.Pause{Kind: live.PauseFailed, Reason: "run 23 failed at step rebuild", Run: 23, Step: "rebuild"})
	out, _, _ = f.run(t, "status")
	if !strings.Contains(out, ": on, paused: run 23 failed at step rebuild\n  cleanup: suggest\n") {
		t.Fatalf("%s", out)
	}
	live.WritePause(f.nb, f.repo, live.Pause{Kind: live.PauseTrust, Reason: "plan needs trust"})
	if out, _, _ = f.run(t, "status"); !strings.Contains(out, "on, paused: plan needs trust\n") {
		t.Fatalf("%s", out)
	}
	live.WritePause(f.nb, f.repo, live.Pause{Kind: live.PauseRate, Reason: "rate limit: 12 live runs in the last hour"})
	if out, _, _ = f.run(t, "status"); !strings.Contains(out, "on, paused: rate limit: 12 live runs in the last hour\n") {
		t.Fatalf("%s", out)
	}
	live.Resume(f.nb, f.repo)
	if out, _, _ = f.run(t, "status"); !strings.Contains(out, ": on\n  cleanup: suggest\n") {
		t.Fatalf("%s", out)
	}

	// JSON
	out, _, code := f.run(t, "status", "--json")
	var got []liveRepoJSON
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 {
		t.Fatalf("%d %s", code, out)
	}
	g := got[0]
	if !g.Enabled || g.State != "on" || !g.Dashboard || g.Cleanup.Mode != "suggest" || g.Cleanup.GraceHours != 24 || len(g.Targets) != 1 || g.Targets[0].Project != "acme-web-feature-login" {
		t.Fatalf("%+v", g)
	}
}

func TestLiveStatusShowsPendingAndLastRun(t *testing.T) {
	f := newLiveCmdFx(t)
	cfg, _ := config.Load()
	cfg.SetLive(f.repo)
	cfg.Save()
	inst := filepath.Join(f.nb, "acme-web-feature-login")
	os.MkdirAll(inst, 0o700)
	os.WriteFile(filepath.Join(f.repo, "Dockerfile"), []byte("FROM scratch\n"), 0o600)
	os.WriteFile(filepath.Join(inst, "compose.json"), []byte(`{"services":{"web":{"build":{"context":"`+f.repo+`"}}}}`), 0o600)
	os.WriteFile(filepath.Join(inst, live.BaselineFile), []byte(`{"branch":"feature-login","digests":{}}`), 0o600)
	f.targets = []live.Target{{Main: f.repo, Root: f.repo, Project: "acme-web-feature-login", InstanceDir: inst, Branch: "x", State: "running", NewBase: f.nb}}
	out, _, _ := f.run(t, "status")
	if !strings.Contains(out, "    pending: build 1\n") {
		t.Fatalf("%s", out)
	}
}

func TestLiveCleanupWritesTheSetting(t *testing.T) {
	f := newLiveCmdFx(t)
	out, errOut, code := f.run(t, "cleanup", "auto", "--grace", "48", "--delete-branch")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if !strings.HasPrefix(out, "cleanup for "+f.repo+": auto (grace 48 h, delete the local branch)\n") || !strings.Contains(out, "without asking") || !strings.Contains(out, "live mode is off") {
		t.Fatalf("%s", out)
	}
	cfg, _ := config.Load()
	if cl := cfg.CleanupFor(f.repo); cl.Mode != "auto" || cl.GraceHours != 48 || !cl.DeleteBranch {
		t.Fatalf("%+v", cl)
	}
	// Flags after the mode and before REPO work, a later call keeps the grace.
	if _, _, code := f.run(t, "cleanup", f.repo, "--keep-branch"); code == 0 {
		t.Fatal("the mode comes first")
	}
	if _, errOut, code := f.run(t, "cleanup", "suggest", f.repo, "--keep-branch"); code != 0 {
		t.Fatalf("%s", errOut)
	}
	cfg, _ = config.Load()
	if cl := cfg.CleanupFor(f.repo); cl.Mode != "suggest" || cl.GraceHours != 48 || cl.DeleteBranch {
		t.Fatalf("%+v", cl)
	}
	for _, args := range [][]string{{"cleanup", "auto", "--grace", "0"}, {"cleanup", "auto", "--grace", "721"}, {"cleanup", "sometimes"}, {"cleanup", "auto", "--delete-branch", "--keep-branch"}, {"cleanup"}} {
		if _, _, code := f.run(t, args...); code == 0 {
			t.Fatalf("%v must fail", args)
		}
	}
}

func TestLiveRefusesInstanceAndUnknownVerbs(t *testing.T) {
	f := newLiveCmdFx(t)
	_, errOut, code := runCLI(t, "--root", f.repo, "--instance", "sf-x", "live", "status")
	if code == 0 || !strings.Contains(errOut, "--instance is not supported by live") {
		t.Fatalf("%d %s", code, errOut)
	}
	if _, _, code := f.run(t, "bogus"); code == 0 {
		t.Fatalf("%d", code)
	}
	if out, _, code := f.run(t, "--help"); code != 0 || !strings.Contains(out, "live status") {
		t.Fatalf("%d %s", code, out)
	}
}
