package repos

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWalkFixtureTree(t *testing.T) {
	root := tempRoot(t)
	a := mkRepo(t, filepath.Join(root, "Acme", "Client", "alpha"))
	b := mkRepo(t, filepath.Join(root, "beta"))
	mkRepo(t, filepath.Join(root, "node_modules", "dep"))
	mkRepo(t, filepath.Join(root, "Backup", "old"))
	mkRepo(t, filepath.Join(root, ".hidden", "h"))
	mkRepo(t, filepath.Join(root, "l1", "l2", "l3", "deep"))
	// a worktree under .worktrees and one elsewhere under the root: both
	// attribute to alpha / beta, never listed on their own
	git(t, a, "worktree", "add", "-q", "-b", "feat", filepath.Join(a, ".worktrees", "feat"))
	git(t, b, "worktree", "add", "-q", "-b", "other", filepath.Join(root, "elsewhere-wt"))
	// symlink loop
	if err := os.Symlink(root, filepath.Join(root, "Acme", "loop")); err != nil {
		t.Fatal(err)
	}

	got := Walk([]string{root}, []string{"Backup"}, 6)
	want := []string{a, b, filepath.Join(root, "l1", "l2", "l3", "deep")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	// depth limit: deep repo is 4 levels below the root
	got = Walk([]string{root}, []string{"Backup"}, 3)
	if !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("depth 3: got %v", got)
	}
}

func TestWalkWorktreeWhoseMainIsOutsideRootsIsIgnored(t *testing.T) {
	outside := mkRepo(t, filepath.Join(tempRoot(t), "main"))
	root := tempRoot(t)
	git(t, outside, "worktree", "add", "-q", "-b", "x", filepath.Join(root, "wt"))
	if got := Walk([]string{root}, nil, 6); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
