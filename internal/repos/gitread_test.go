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
