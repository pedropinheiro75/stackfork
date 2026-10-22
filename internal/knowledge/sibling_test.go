package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "commit.gpgsign=false", "-c", "user.name=t", "-c", "user.email=t@t"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// repoWithWorktrees makes a main checkout plus one linked worktree per name.
func repoWithWorktrees(t *testing.T, names ...string) (main string, wts map[string]string) {
	t.Helper()
	main, _ = filepath.EvalSymlinks(t.TempDir())
	gitIn(t, main, "init", "-q", "-b", "main")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	wts = map[string]string{}
	for _, n := range names {
		dir := filepath.Join(main, ".worktrees", n)
		gitIn(t, main, "worktree", "add", "-q", "-b", n, dir)
		wts[n] = dir
	}
	return main, wts
}

func record(t *testing.T, dir, status, date string) {
	t.Helper()
	k := &Knowledge{Status: status, Plan: Plan{Status: PlanRecorded, Modes: []string{ModeContainer},
		Steps: []Step{{ID: "up", Kind: KindComposeUp}}}}
	if status == StatusVerified {
		k.Verification = &Verification{Date: date, Confirmation: "works"}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".stackfork"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := k.Save(filepath.Join(dir, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadForOrder(t *testing.T) {
	main, wts := repoWithWorktrees(t, "a", "b", "c")
	a, b, c := wts["a"], wts["b"], wts["c"]

	// nothing anywhere
	if k, src, err := LoadFor(c, main); k != nil || src != "" || err != nil {
		t.Fatalf("empty: %v %q %v", k, src, err)
	}
	// a draft sibling is not borrowed
	record(t, a, StatusDraft, "")
	if k, _, _ := LoadFor(c, main); k != nil {
		t.Fatal("a draft in another worktree must not be borrowed")
	}
	// two verified siblings: the newest verification wins
	record(t, a, StatusVerified, "2026-01-01")
	record(t, b, StatusVerified, "2026-03-01")
	k, src, err := LoadFor(main, main)
	if err != nil || k == nil || src != b {
		t.Fatalf("newest: %v %q %v", k, src, err)
	}
	// same date: the newer file wins
	record(t, a, StatusVerified, "2026-03-01")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(a, ".stackfork", "knowledge.json"), later, later); err != nil {
		t.Fatal(err)
	}
	if _, src, _ = LoadFor(main, main); src != a {
		t.Fatalf("tie: %q", src)
	}
	// main beats a sibling; own beats main
	record(t, main, StatusDraft, "")
	if _, src, _ = LoadFor(c, main); src != main {
		t.Fatalf("main: %q", src)
	}
	record(t, c, StatusDraft, "")
	if _, src, _ = LoadFor(c, main); src != c {
		t.Fatalf("own: %q", src)
	}
}
