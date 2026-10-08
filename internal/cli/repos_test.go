package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scratchRepo makes a one-commit repo on main at dir.
func scratchRepo(t *testing.T, dir string) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	for _, args := range [][]string{{"add", "f"}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	r, _ := filepath.EvalSymlinks(dir)
	return r
}

func reposEnv(t *testing.T) (root, stateDir string) {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	installFakeDocker(t)
	root = t.TempDir()
	stateDir = t.TempDir()
	return
}

func TestReposNoRootsExplainsHowToAddOne(t *testing.T) {
	_, state := reposEnv(t)
	out, _, code := runCLI(t, "--state-dir", state, "--legacy-state-dir", t.TempDir(), "repos")
	if code != 0 || !strings.Contains(out, "stackfork config add-root PATH") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestReposListsJSONTableAndPrompt(t *testing.T) {
	root, state := reposEnv(t)
	repo := scratchRepo(t, filepath.Join(root, "Group", "proj"))
	if _, e, code := runCLI(t, "config", "add-root", root); code != 0 {
		t.Fatal(e)
	}
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "repos", "--json")...)
	if code != 0 {
		t.Fatal(e)
	}
	var res struct {
		Repos []struct{ Name, Path, Group, Readiness string }
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Repos) != 1 {
		t.Fatalf("%v %s", err, out)
	}
	if got := res.Repos[0]; got.Name != "proj" || got.Path != repo || got.Group != "Group" || got.Readiness != "unknown" {
		t.Fatalf("%+v", got)
	}
	table, _, _ := runCLI(t, append(base, "repos")...)
	if !strings.Contains(table, "proj") || !strings.Contains(table, "unknown") {
		t.Fatalf("%s", table)
	}
	p, _, code := runCLI(t, append(base, "repos", "--prompt", "proj", "feat/x")...)
	if code != 0 || !strings.Contains(p, "`proj`") || !strings.Contains(p, "feat/x") || !strings.Contains(p, "stackfork worktree add") {
		t.Fatalf("%d %s", code, p)
	}
	if _, e, code := runCLI(t, append(base, "repos", "--prompt", "nope", "x")...); code == 0 || !strings.Contains(e, "no repo") {
		t.Fatalf("%d %s", code, e)
	}
	if _, _, code := runCLI(t, append(base, "repos", "--rescan")...); code != 0 {
		t.Fatal("rescan failed")
	}
}

func TestWorktreeAddByNameAndPath(t *testing.T) {
	root, state := reposEnv(t)
	repo := scratchRepo(t, filepath.Join(root, "proj"))
	runCLI(t, "config", "add-root", root)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}

	out, e, code := runCLI(t, append(base, "worktree", "add", "proj", "feat/x", "--from", "main")...)
	if code != 0 || !strings.Contains(out, filepath.Join(repo, ".worktrees", "feat-x")) {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "feat-x", "f")); err != nil {
		t.Fatal(err)
	}
	if _, e, code := runCLI(t, append(base, "worktree", "add", repo, "feat/x")...); code == 0 || !strings.Contains(e, "already exists") {
		t.Fatalf("%d %s", code, e)
	}
	if _, e, code := runCLI(t, append(base, "worktree", "add", "nope", "x")...); code == 0 || !strings.Contains(e, "no repo") {
		t.Fatalf("%d %s", code, e)
	}
	if _, e, code := runCLI(t, append(base, "worktree", "add", "proj")...); code == 0 || !strings.Contains(e, "usage") {
		t.Fatalf("%d %s", code, e)
	}
}
