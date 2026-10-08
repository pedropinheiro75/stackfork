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

const readyCompose = "services:\n  db:\n    image: postgres:17\n"

// readyRepo is a scratch repo with an adapter compose file, so it is ready.
func readyRepo(t *testing.T, root string) string {
	t.Helper()
	repo := scratchRepo(t, filepath.Join(root, "ready"))
	os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(readyCompose), 0o644)
	return repo
}

func countLines(log, sub string) int { return strings.Count(log, sub) }

func TestStartEnvPreparesOnlyWhenNeededThenUps(t *testing.T) {
	root, state := reposEnv(t)
	repo := readyRepo(t, root)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}

	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if !strings.Contains(out, "preparing") || !strings.Contains(out, "started") {
		t.Fatalf("progress lines missing: %s", out)
	}
	b, _ := os.ReadFile(log)
	if countLines(string(b), " up -d ") != 1 || countLines(string(b), " config ") != 1 {
		t.Fatalf("first start: %s", b)
	}

	out, _, code = runCLI(t, append(base, "start-env", repo)...)
	b, _ = os.ReadFile(log)
	if code != 0 || !strings.Contains(out, "already prepared") || countLines(string(b), " config ") != 1 || countLines(string(b), " up -d ") != 2 {
		t.Fatalf("second start must not re-prepare: %d %s\n%s", code, out, b)
	}

	// an input change forces prepare again
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(readyCompose+"  cache:\n    image: redis:7\n"), 0o644)
	out, _, code = runCLI(t, append(base, "start-env", repo)...)
	b, _ = os.ReadFile(log)
	if code != 0 || !strings.Contains(out, "preparing") || countLines(string(b), " config ") != 2 {
		t.Fatalf("changed input must re-prepare: %d %s\n%s", code, out, b)
	}
}

func TestStartEnvRefusesUnknownWithoutStartingAnything(t *testing.T) {
	root, state := reposEnv(t)
	repo := scratchRepo(t, filepath.Join(root, "mystery"))
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "unknown") || !strings.Contains(e, "repos --prompt") {
		t.Fatalf("%d %s", code, e)
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), " up ") || strings.Contains(string(b), " config ") {
		t.Fatalf("docker was used for an unknown repo:\n%s", b)
	}
}

func TestRecipePrepareArgs(t *testing.T) {
	got := recipePrepareArgs(`stackfork --root "$CHECKOUT" prepare -f .agent-dev/a.compose.yaml --env-file '.agent-dev/runtime.env' --random-ports`)
	want := []string{"-f", ".agent-dev/a.compose.yaml", "--env-file", ".agent-dev/runtime.env", "--random-ports"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", got)
	}
	for _, bad := range []string{"stackfork prepare -f $X/a.yaml", "stackfork prepare --npmrc /x", "stackfork up", "stackfork prepare -f a.yaml && rm -rf x"} {
		if recipePrepareArgs(bad) != nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestStartEnvInWorktreeMissingAnUntrackedAdapterSaysSo(t *testing.T) {
	root, state := reposEnv(t)
	repo := readyRepo(t, root) // the adapter is untracked: a new worktree will not have it
	installFakeDocker(t)
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "wt", filepath.Join(repo, ".worktrees", "wt")).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", filepath.Join(repo, ".worktrees", "wt"))...)
	if code == 0 || !strings.Contains(e, "only in the main checkout") || !strings.Contains(e, "adapter_files") {
		t.Fatalf("%d %s", code, e)
	}
}
