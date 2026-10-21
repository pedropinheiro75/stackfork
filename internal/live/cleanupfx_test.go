package live

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
)

// cleanupFx is a repository with a bare origin whose feature-login branch was
// squash-merged into origin/main and deleted there, a state dir, and an
// instance for the branch's worktree.
type cleanupFx struct {
	Main, Origin, WT string
	State            string
	Inst             Target
}

func newCleanupFx(t *testing.T) *cleanupFx {
	t.Helper()
	root := tmp(t)
	f := &cleanupFx{Main: filepath.Join(root, "acme-web"), Origin: filepath.Join(root, "origin.git"), State: filepath.Join(root, "state")}
	git(t, root, "init", "-q", "--bare", "-b", "main", f.Origin)
	os.MkdirAll(f.Main, 0o755)
	git(t, f.Main, "init", "-q", "-b", "main")
	write(t, f.Main, "app.txt", "1\n")
	git(t, f.Main, "add", "-A")
	git(t, f.Main, "commit", "-q", "-m", "init")
	git(t, f.Main, "remote", "add", "origin", f.Origin)
	git(t, f.Main, "push", "-q", "-u", "origin", "main")
	wt := filepath.Join(f.Main, ".worktrees", "feature-login")
	git(t, f.Main, "worktree", "add", "-q", "-b", "feature-login", wt, "main")
	f.WT, _ = filepath.EvalSymlinks(wt)
	write(t, f.WT, "login.txt", "x\n")
	git(t, f.WT, "add", "-A")
	git(t, f.WT, "commit", "-q", "-m", "login")
	git(t, f.WT, "push", "-q", "-u", "origin", "feature-login")
	write(t, f.Main, "other.txt", "o\n")
	git(t, f.Main, "add", "-A")
	git(t, f.Main, "commit", "-q", "-m", "unrelated")
	git(t, f.Main, "merge", "-q", "--squash", "feature-login")
	git(t, f.Main, "commit", "-q", "-m", "feature-login (#42)")
	git(t, f.Main, "push", "-q", "origin", "main")
	git(t, f.Main, "push", "-q", "origin", "--delete", "feature-login")
	if err := cleanup.Fetch(f.Main, "origin", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	f.Inst = Target{Main: f.Main, Root: f.WT, Project: "acme-web-feature-login", InstanceDir: filepath.Join(f.State, "acme-web-feature-login"),
		Branch: "feature-login", State: "stopped", NewBase: f.State}
	os.MkdirAll(f.Inst.InstanceDir, 0o700)
	return f
}

// job is the cleanup job of the fixture's candidate.
func (f *cleanupFx) job(t *testing.T) CleanupJob {
	t.Helper()
	cs, err := cleanup.Candidates(f.Main)
	if err != nil || len(cs) != 1 {
		t.Fatalf("candidates: %+v %v", cs, err)
	}
	res := cleanup.Detect(f.Main, cs)[0]
	if res.State != cleanup.Merged {
		t.Fatalf("not merged: %+v", res)
	}
	inst := f.Inst
	return CleanupJob{Main: f.Main, NewBase: f.State, Candidate: cs[0], Result: res, Instance: &inst, Down: func(io.Writer) error { return nil }}
}
