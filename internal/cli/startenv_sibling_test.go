package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const (
	sibCanary    = "CANARY-runtime-value-7731"
	sibTokenName = "SF_TEST_SIBLING_TOKEN"
)

// siblingRepo builds a scratch repo whose sibling worktree is prepared with
// a tracked compose file, an untracked .agent-dev adapter + runtime.env and a
// secret reference (the maintainer's failing app-d layout).
func siblingRepo(t *testing.T) (repo, sib string, base []string) {
	t.Helper()
	root, state := reposEnv(t)
	t.Setenv(sibTokenName, "never-written-anywhere")
	repo = scratchRepo(t, filepath.Join(root, "proj"))
	os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte("services:\n  app:\n    image: alpine:3.20\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "compose"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	base = []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "worktree", "add", repo, "feat-a", "--from", "main")...); code != 0 {
		t.Fatal(e)
	}
	sib = filepath.Join(repo, ".worktrees", "feat-a")
	ad := filepath.Join(sib, ".agent-dev")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "compose.yml"), []byte("services:\n  app:\n    env_file: ./.agent-dev/runtime.env\n    secrets: [tok]\nsecrets:\n  tok:\n    environment: tok\n"), 0o644)
	os.WriteFile(filepath.Join(ad, "start.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(ad, "runtime.env"), []byte("DEV_CHECKOUT="+sib+"\nCANARY="+sibCanary+"\n"), 0o600)
	os.WriteFile(filepath.Join(ad, ".env"), []byte("X="+sibCanary), 0o600)
	os.WriteFile(filepath.Join(ad, "id_rsa"), []byte(sibCanary), 0o600)
	_, e, code := runCLI(t, append(append([]string{}, base...), "--root", sib, "--secret", "tok=env:"+sibTokenName,
		"prepare", "-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "--env-file", ".agent-dev/runtime.env")...)
	if code != 0 {
		t.Fatalf("prepare sibling: %s", e)
	}
	// learn was verified in that worktree (the usual place an agent learns)
	if err := (&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(sib, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return
}

func TestStartEnvInNewWorktreeReusesPreparedSiblingRecipe(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	log := installFakeDocker(t)
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	// before starting, the new worktree has none of the untracked files
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	for _, want := range []string{"reusing the recipe", "copied .agent-dev/compose.yml", "copied .agent-dev/start.sh", "rewrote DEV_CHECKOUT", "reusing secret references tok", "started"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, sibCanary) || strings.Contains(e, sibCanary) {
		t.Error("a runtime value leaked into the output")
	}
	ad := filepath.Join(nw, ".agent-dev")
	if env, _ := os.ReadFile(filepath.Join(ad, "runtime.env")); !strings.Contains(string(env), "DEV_CHECKOUT="+nw) || !strings.Contains(string(env), sibCanary) || strings.Contains(string(env), sib) {
		t.Errorf("runtime.env: %s", env)
	}
	for _, f := range []string{".env", "id_rsa"} {
		if _, err := os.Lstat(filepath.Join(ad, f)); err == nil {
			t.Errorf("%s was copied", f)
		}
	}
	// the new instance has its own identity, files in its own root, same secret reference
	whereOut, _, _ := runCLI(t, append(base, "--root", nw, "where")...)
	var w struct{ State string }
	json.Unmarshal([]byte(whereOut), &w)
	m, err := os.ReadFile(filepath.Join(w.State, "manifest.json"))
	if err != nil {
		t.Fatalf("%v\n%s", err, whereOut)
	}
	var man struct {
		Root    string
		Files   []string
		EnvFile string `json:"env_file"`
		Secrets map[string]string
	}
	json.Unmarshal(m, &man)
	if man.Root != nw || len(man.Files) != 2 || !strings.HasPrefix(man.Files[1], nw) || !strings.HasPrefix(man.EnvFile, nw) || man.Secrets["tok"] != "env:"+sibTokenName {
		t.Errorf("manifest %+v", man)
	}
	if strings.Contains(string(m), sibCanary) {
		t.Error("manifest holds a runtime value")
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), " up -d ") {
		t.Errorf("never started: %s", b)
	}
	// a worktree created while a sibling is prepared is seeded right away
	out, _, code = runCLI(t, append(base, "worktree", "add", repo, "feat-c", "--from", "main")...)
	if code != 0 || !strings.Contains(out, "copied .agent-dev/compose.yml") {
		t.Errorf("worktree add did not seed: %d\n%s", code, out)
	}
}

func TestStartEnvExplainsWhySiblingRecipeCannotBeReused(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	// the sibling's compose file is tracked only on its own branch
	os.WriteFile(filepath.Join(sib, "extra.yml"), []byte("services: {}\n"), 0o644)
	for _, args := range [][]string{{"add", "extra.yml"}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", sib}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s", out)
		}
	}
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", sib, "--secret", "tok=env:"+sibTokenName,
		"prepare", "-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "-f", "extra.yml", "--env-file", ".agent-dev/runtime.env")...); code != 0 {
		t.Fatal(e)
	}
	if _, e, code := runCLI(t, append(base, "worktree", "add", repo, "feat-b", "--from", "main")...); code != 0 {
		t.Fatal(e)
	}
	_, e, code := runCLI(t, append(base, "start-env", filepath.Join(repo, ".worktrees", "feat-b"))...)
	if code == 0 || !strings.Contains(e, "extra.yml") || !strings.Contains(e, "cannot be reused") {
		t.Fatalf("%d %s", code, e)
	}
}
