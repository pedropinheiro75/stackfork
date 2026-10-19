package repos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadGitBranchesWorktreesDirtyAheadBehind(t *testing.T) {
	root := tempRoot(t)
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	repo := mkRepo(t, filepath.Join(root, "work", "proj"))
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-q", "-u", "origin", "main")
	git(t, repo, "remote", "set-head", "origin", "main")
	// local main ahead by 1
	os.WriteFile(filepath.Join(repo, "g"), []byte("y"), 0o644)
	git(t, repo, "add", "g")
	git(t, repo, "commit", "-q", "-m", "second")
	// a branch without a worktree, and one with
	git(t, repo, "branch", "idle")
	wt := filepath.Join(repo, ".worktrees", "feat")
	git(t, repo, "worktree", "add", "-q", "-b", "feat", wt)
	os.WriteFile(filepath.Join(wt, "f"), []byte("dirty"), 0o644)

	d, err := readGit(repo)
	if err != nil {
		t.Fatal(err)
	}
	if d.DefaultBranch != "main" {
		t.Fatalf("default %q", d.DefaultBranch)
	}
	if len(d.Worktrees) != 2 || !d.Worktrees[0].Main || d.Worktrees[0].Path != repo || d.Worktrees[0].Branch != "main" || d.Worktrees[0].Dirty {
		t.Fatalf("worktrees %+v", d.Worktrees)
	}
	if d.Worktrees[1].Branch != "feat" || !d.Worktrees[1].Dirty {
		t.Fatalf("feat worktree %+v", d.Worktrees[1])
	}
	if len(d.Branches) != 1 || d.Branches[0].Name != "idle" {
		t.Fatalf("branches %+v", d.Branches)
	}
	if d.LastCommit.IsZero() {
		t.Fatal("no last commit")
	}
	// main is checked out (so not in Branches); ahead/behind parse via helper
	if a, b := parseTrack("ahead 2, behind 3"); a != 2 || b != 3 {
		t.Fatal("parseTrack")
	}
}

func TestReadGitNeverContactsARemote(t *testing.T) {
	root := tempRoot(t)
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	repo := mkRepo(t, filepath.Join(root, "proj"))
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-q", "-u", "origin", "main")
	log := installGitLog(t)
	if _, err := readGit(repo); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, log)
	if got == "" {
		t.Fatal("wrapper saw no git calls; the test proves nothing")
	}
	for _, bad := range []string{" fetch", " ls-remote", " pull", " push", " clone", " remote update"} {
		if strings.Contains(got, bad) {
			t.Fatalf("discovery ran a network git command (%s):\n%s", bad, got)
		}
	}
}

func TestWorktreeAheadBehindFromUpstream(t *testing.T) {
	root := tempRoot(t)
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	repo := mkRepo(t, filepath.Join(root, "proj"))
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-q", "-u", "origin", "main")

	wt := filepath.Join(repo, ".worktrees", "feat")
	git(t, repo, "worktree", "add", "-q", "-b", "feat", wt)
	git(t, wt, "push", "-q", "-u", "origin", "feat")
	// 1 commit only upstream (pushed from a clone), 2 commits only local
	clone := filepath.Join(root, "clone")
	git(t, root, "clone", "-q", "-b", "feat", origin, clone)
	os.WriteFile(filepath.Join(clone, "up"), []byte("u"), 0o644)
	git(t, clone, "add", "up")
	git(t, clone, "commit", "-q", "-m", "upstream only")
	git(t, clone, "push", "-q", "origin", "feat")
	git(t, wt, "fetch", "-q", "origin")
	for _, n := range []string{"l1", "l2"} {
		os.WriteFile(filepath.Join(wt, n), []byte(n), 0o644)
		git(t, wt, "add", n)
		git(t, wt, "commit", "-q", "-m", n)
	}

	d, err := readGit(repo)
	if err != nil {
		t.Fatal(err)
	}
	var feat, main Worktree
	for _, w := range d.Worktrees {
		if w.Branch == "feat" {
			feat = w
		} else if w.Main {
			main = w
		}
	}
	if feat.Ahead != 2 || feat.Behind != 1 || feat.Upstream != "origin/feat" || feat.LastCommit.IsZero() {
		t.Fatalf("feat worktree %+v", feat)
	}
	if main.Ahead != 0 || main.Behind != 0 || main.Upstream != "origin/main" {
		t.Fatalf("main worktree %+v", main)
	}

	// no upstream: 0/0 and an empty upstream
	wt2 := filepath.Join(repo, ".worktrees", "local")
	git(t, repo, "worktree", "add", "-q", "-b", "local", wt2)
	d, _ = readGit(repo)
	for _, w := range d.Worktrees {
		if w.Branch == "local" && (w.Ahead != 0 || w.Behind != 0 || w.Upstream != "") {
			t.Fatalf("local worktree %+v", w)
		}
	}
}
