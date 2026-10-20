package cleanup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	for k, v := range map[string]string{
		"GIT_CONFIG_COUNT": "2", "GIT_CONFIG_KEY_0": "commit.gpgsign", "GIT_CONFIG_VALUE_0": "false",
		"GIT_CONFIG_KEY_1": "init.defaultBranch", "GIT_CONFIG_VALUE_1": "main",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		os.Setenv(k, v)
	}
}

func tmp(t *testing.T) string {
	t.Helper()
	r, _ := filepath.EvalSymlinks(t.TempDir())
	return r
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, rel, body, msg string) {
	t.Helper()
	put(t, dir, rel, body)
	run(t, dir, "add", rel)
	run(t, dir, "commit", "-q", "-m", msg)
}

// fixture is a repository with a bare origin, both under a temp dir.
type fixture struct{ Main, Origin string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := tmp(t)
	f := fixture{Main: filepath.Join(root, "acme-web"), Origin: filepath.Join(root, "origin.git")}
	run(t, root, "init", "-q", "--bare", "-b", "main", f.Origin)
	os.MkdirAll(f.Main, 0o755)
	run(t, f.Main, "init", "-q", "-b", "main")
	commit(t, f.Main, "app.txt", "1\n", "init")
	run(t, f.Main, "remote", "add", "origin", f.Origin)
	run(t, f.Main, "push", "-q", "-u", "origin", "main")
	return f
}

// branch creates a worktree for a new pushed branch with n commits, each
// adding its own file.
func (f fixture) branch(t *testing.T, name string, n int) string {
	t.Helper()
	wt := filepath.Join(f.Main, ".worktrees", name)
	run(t, f.Main, "worktree", "add", "-q", "-b", name, wt, "main")
	for i := 0; i < n; i++ {
		commit(t, wt, name+"-"+string(rune('a'+i))+".txt", "x\n", name+" "+string(rune('a'+i)))
	}
	run(t, wt, "push", "-q", "-u", "origin", name)
	r, _ := filepath.EvalSymlinks(wt)
	return r
}

func rmAll(t *testing.T, p string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
}
