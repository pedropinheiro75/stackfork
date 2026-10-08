package repos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func writeKnowledge(t *testing.T, dir, status string, inputs ...knowledge.Input) {
	t.Helper()
	k := &knowledge.Knowledge{Status: status, Inputs: inputs}
	if err := k.Save(filepath.Join(dir, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
}

func TestReadiness(t *testing.T) {
	root := tempRoot(t)
	mk := func(n string) string { return mkRepo(t, filepath.Join(root, n)) }

	verified := mk("verified")
	writeKnowledge(t, verified, knowledge.StatusVerified)

	stale := mk("stale")
	writeKnowledge(t, stale, knowledge.StatusVerified, knowledge.Input{Path: "f", SHA256: "0000"})

	draft := mk("draft")
	writeKnowledge(t, draft, knowledge.StatusDraft)

	adapter := mk("adapter")
	os.MkdirAll(filepath.Join(adapter, ".agent-dev"), 0o755)
	os.WriteFile(filepath.Join(adapter, ".agent-dev", "app.compose.yaml"), []byte("services: {}"), 0o644)

	prepared := mk("prepared")
	unknown := mk("mystery")

	profiles := t.TempDir()
	os.MkdirAll(filepath.Join(profiles, "p", "references"), 0o755)
	os.WriteFile(filepath.Join(profiles, "p", "references", "profiled-app.md"), nil, 0o644)
	profiled := mk("profiled-app")

	insts := []inventory.Instance{{Project: "sf-prepared-1", Root: prepared, Branch: "main"}}
	cases := []struct {
		name, path, want, reasonHas string
	}{
		{"verified", verified, Ready, "verified knowledge"},
		{"stale", stale, Ready, "stale"},
		{"draft", draft, Draft, "draft"},
		{"adapter", adapter, Ready, "adapter"},
		{"prepared", prepared, Ready, "sf-prepared-1"},
		{"profiled-app", profiled, Ready, "profile"},
		{"mystery", unknown, Unknown, "never learned"},
	}
	for _, c := range cases {
		got, reason := ReadinessOf(c.name, []string{c.path}, insts, []string{filepath.Join(profiles, "p")})
		if got != c.want || !strings.Contains(reason, c.reasonHas) {
			t.Errorf("%s: got %s %q, want %s containing %q", c.name, got, reason, c.want, c.reasonHas)
		}
	}
}

func TestScanJoinsInstancesGroupsAndCounts(t *testing.T) {
	root := tempRoot(t)
	a := mkRepo(t, filepath.Join(root, "Acme", "alpha"))
	mkRepo(t, filepath.Join(root, "top"))
	git(t, a, "worktree", "add", "-q", "-b", "feat", filepath.Join(a, ".worktrees", "feat"))
	wt, _ := filepath.EvalSymlinks(filepath.Join(a, ".worktrees", "feat"))

	cfg := &config.Config{Roots: []string{root}}
	res := Scan(Options{Config: cfg, Instances: []inventory.Instance{
		{Project: "sf-feat-1", Root: wt, Branch: "feat", State: inventory.StateRunning},
	}})
	if !res.Configured || len(res.Roots) != 1 || res.Roots[0].Repos != 2 || len(res.Repos) != 2 {
		t.Fatalf("%+v", res)
	}
	var alpha, top *Repo
	for i := range res.Repos {
		switch res.Repos[i].Name {
		case "alpha":
			alpha = &res.Repos[i]
		case "top":
			top = &res.Repos[i]
		}
	}
	if alpha.Group != "Acme" || top.Group != "" || alpha.Running != 1 {
		t.Fatalf("alpha=%+v top=%+v", alpha, top)
	}
	if len(alpha.Worktrees) != 2 || alpha.Worktrees[1].Instance == nil || alpha.Worktrees[1].Instance.Project != "sf-feat-1" {
		t.Fatalf("worktrees %+v", alpha.Worktrees)
	}
}

func TestScanWithNoRootsScansNothing(t *testing.T) {
	logPath := installGitLog(t)
	res := Scan(Options{Config: &config.Config{}})
	if res.Configured || len(res.Repos) != 0 || readLog(t, logPath) != "" {
		t.Fatalf("%+v", res)
	}
}

func TestSecondScanOfUnchangedRepoRunsNoGit(t *testing.T) {
	root := tempRoot(t)
	repo := mkRepo(t, filepath.Join(root, "proj"))
	cfg := &config.Config{Roots: []string{root}}
	cacheFile := filepath.Join(t.TempDir(), "repos-cache.json")
	logPath := installGitLog(t)

	first := Scan(Options{Config: cfg, CacheFile: cacheFile})
	if readLog(t, logPath) == "" || len(first.Repos) != 1 {
		t.Fatalf("first scan should have run git: %+v", first)
	}
	os.Truncate(logPath, 0)
	second := Scan(Options{Config: cfg, CacheFile: cacheFile})
	if got := readLog(t, logPath); got != "" {
		t.Fatalf("unchanged repo re-read:\n%s", got)
	}
	if len(second.Repos) != 1 || second.Repos[0].Worktrees[0].Branch != "main" {
		t.Fatalf("%+v", second)
	}

	// a change in the repo's refs is picked up
	git(t, repo, "branch", "newbranch")
	third := Scan(Options{Config: cfg, CacheFile: cacheFile})
	if got := readLog(t, logPath); got == "" || len(third.Repos[0].Branches) != 1 {
		t.Fatalf("changed repo not re-read: %q %+v", got, third.Repos[0].Branches)
	}

	// --rescan forces a re-read even when nothing changed
	os.Truncate(logPath, 0)
	Scan(Options{Config: cfg, CacheFile: cacheFile, Rescan: true})
	if readLog(t, logPath) == "" {
		t.Fatal("rescan must re-read")
	}
	if fi, err := os.Stat(cacheFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache file: %v %v", fi, err)
	}
}
