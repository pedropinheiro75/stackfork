package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// TestLiveBranchCleanup: with real Docker and git, a branch squash-merged
// into a temp bare origin and deleted there is detected; a cancelled dialog
// deletes nothing; a confirmed cleanup removes the worktree, the instance's
// containers and volumes and the local branch, and the volume of the
// sibling instance (the main checkout's) is untouched. Temp repositories,
// temp state dirs, a test proxy name and a test config dir; alpine:latest is
// already on the machine, nothing is pulled.
func TestLiveBranchCleanup(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-cleanup")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())

	before := countDocker(t)
	scratch, _ := filepath.EvalSymlinks(t.TempDir())
	stateDir, legacy := filepath.Join(scratch, "state"), filepath.Join(scratch, "legacy")
	global := []string{"--state-dir", stateDir, "--legacy-state-dir", legacy}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}
	project := func(root string) string {
		t.Helper()
		out, code := sf("--root", root, "where")
		if code != 0 {
			t.Fatalf("where: %s", out)
		}
		i := strings.Index(out, `"project": "`)
		j := strings.Index(out[i+12:], `"`)
		return out[i+12 : i+12+j]
	}

	origin := filepath.Join(scratch, "origin.git")
	mustRun(t, "git", "init", "-q", "--bare", "-b", "main", origin)
	repo := filepath.Join(scratch, "cleanuprepo")
	os.MkdirAll(repo, 0o755)
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	if err := os.WriteFile(filepath.Join(repo, "docker-compose.yml"),
		[]byte("services:\n  app:\n    image: alpine:latest\n    command: [\"sleep\", \"3600\"]\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", "docker-compose.yml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	}}}
	if err := verifiedFixture(k).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "init")
	gitT(t, repo, "remote", "add", "origin", origin)
	gitT(t, repo, "push", "-q", "-u", "origin", "main")
	wtRes, err := repos.AddWorktree(repo, "feature-login", "main")
	if err != nil {
		t.Fatal(err)
	}
	wt := wtRes.Path
	if err := os.WriteFile(filepath.Join(wt, "login.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, wt, "add", "-A")
	gitT(t, wt, "commit", "-q", "-m", "login")
	gitT(t, wt, "push", "-q", "-u", "origin", "feature-login")

	pMain, pWT := project(repo), project(wt)
	projects := []string{pMain, pWT}
	defer func() {
		sf("--root", repo, "down", "--volumes")
		for _, p := range projects {
			exec.Command("docker", "rm", "-f", p+"-app-1").Run()
			exec.Command("docker", "volume", "rm", p+"_data").Run()
			exec.Command("docker", "network", "rm", p+"_default").Run()
		}
		before.assertBaseline(t, func() dockerCounts { return countDocker(t) }, projects)
	}()
	for _, root := range []string{repo, wt} {
		if out, code := sf("start-env", root); code != 0 {
			t.Fatalf("start-env %s: %s", root, out)
		}
		if out, code := sf("--root", root, "exec", "app", "sh", "-c", "touch /data/marker"); code != 0 {
			t.Fatalf("marker: %s", out)
		}
	}

	// squash-merge into main, push, and delete the branch on origin
	gitT(t, repo, "merge", "-q", "--squash", "feature-login")
	gitT(t, repo, "commit", "-q", "-m", "feature-login (#42)")
	gitT(t, repo, "push", "-q", "origin", "main")
	gitT(t, repo, "push", "-q", "origin", "--delete", "feature-login")
	if err := cleanup.Fetch(repo, "origin", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	liveOnFor(t, repo, &config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true})
	if out, code := sf("config", "add-root", scratch); code != 0 {
		t.Fatalf("add-root: %s", out)
	}

	// detected, and the preview deletes nothing
	out, code := sf("live", "status", repo)
	if code != 0 || !strings.Contains(out, "feature-login: merged") {
		t.Fatalf("status (%d):\n%s", code, out)
	}
	out, code = sf("live", "clean", repo, "feature-login")
	if code != 0 || !strings.Contains(out, pWT+"_data") || strings.Contains(out, pMain+"_data") || !strings.Contains(out, "Nothing was deleted") {
		t.Fatalf("preview (%d):\n%s", code, out)
	}
	if !dockerHas(t, "volume", pWT+"_data") {
		t.Fatal("the preview deleted the volume")
	}

	// the page's dialog: cancelled first, then confirmed
	srv, err := dashboard.New(stateDir, legacy, Version, 29431)
	if err != nil {
		t.Fatal(err)
	}
	srv.LiveCleanup = dashboardCleanup(stateDir, legacy)
	var asked []string
	answer := false
	old := dashboard.Confirm
	dashboard.Confirm = func(_, text string) (bool, error) { asked = append(asked, text); return answer, nil }
	defer func() { dashboard.Confirm = old }()
	post := func() (string, int) {
		b, _ := json.Marshal(map[string]string{"repo": repo, "op": "clean", "path": wt})
		req := httptest.NewRequest("POST", "/api/live", bytes.NewReader(b))
		req.Host = fmt.Sprintf("127.0.0.1:%d", 29431)
		req.Header.Set("X-Stackfork-Token", srv.Token)
		req.Header.Set("Origin", "http://"+req.Host)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Body.String(), rec.Code
	}
	if body, code := post(); code != 200 || !strings.Contains(body, "cancelled") || !dockerHas(t, "volume", pWT+"_data") {
		t.Fatalf("cancelled (%d): %s", code, body)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("a cancelled dialog removed the worktree")
	}
	answer = true
	body, code := post()
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("confirmed (%d): %s", code, body)
	}
	if len(asked) != 2 || !strings.Contains(asked[1], pWT+"_data") || strings.Contains(asked[1], pMain+"_data") {
		t.Fatalf("the dialog must name this instance's volume only: %v", asked)
	}

	// the worktree, its containers, volume and branch are gone
	if _, err := os.Stat(wt); err == nil {
		t.Error("the worktree is still there")
	}
	if dockerHas(t, "volume", pWT+"_data") || dockerHas(t, "container", pWT+"-app-1") {
		t.Error("the instance's volume or container is still there")
	}
	if gitT(t, repo, "branch", "--list", "feature-login") != "" {
		t.Error("the local branch is still there")
	}
	list, _ := runs.List(filepath.Join(stateDir, pWT))
	var cleanupRun *runs.Run
	for _, r := range list {
		if r.Cause != nil && r.Cause.Cleanup != nil {
			cleanupRun = r
		}
	}
	if cleanupRun == nil || cleanupRun.Outcome != runs.Succeeded || len(cleanupRun.Steps) != 3 {
		t.Fatalf("cleanup run: %+v", cleanupRun)
	}

	// the sibling instance is untouched
	if !dockerHas(t, "volume", pMain+"_data") || !dockerHas(t, "container", pMain+"-app-1") {
		t.Fatal("the sibling instance lost its volume or container")
	}
	if out, code := sf("--root", repo, "exec", "app", "ls", "/data/marker"); code != 0 {
		t.Fatalf("the sibling's data is gone: %s", out)
	}
	for v := range toSet(before.volumes) {
		if !toSet(countDocker(t).volumes)[v] {
			t.Errorf("volume %s from before the test is gone", v)
		}
	}
}
