package cli

import (
	"encoding/json"
	"fmt"
	"io"
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
		"prepare", "--force", "-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "--env-file", ".agent-dev/runtime.env")...)
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

// A worktree that has its own adapter is prepared from it, not from the
// sibling, but it still needs the registry token the sibling runs with:
// without the reference, up fails late on "NPM_TOKEN ... is not set".
func TestStartEnvWithOwnAdapterReusesSiblingSecretReferences(t *testing.T) {
	repo, _, base := siblingRepo(t)
	installFakeDocker(t)
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	ad := filepath.Join(nw, ".stackfork")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "app.compose.yaml"), []byte("services:\n  app:\n    image: alpine:3.20\n    secrets: [tok]\nsecrets:\n  tok:\n    environment: tok\n"), 0o644)
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if !strings.Contains(out, "reusing secret references tok from another worktree") || strings.Contains(out, "reusing the recipe") {
		t.Errorf("output:\n%s", out)
	}
	whereOut, _, _ := runCLI(t, append(base, "--root", nw, "where")...)
	var w struct{ State string }
	json.Unmarshal([]byte(whereOut), &w)
	m, _ := os.ReadFile(filepath.Join(w.State, "manifest.json"))
	var man struct{ Secrets map[string]string }
	json.Unmarshal(m, &man)
	if man.Secrets["tok"] != "env:"+sibTokenName {
		t.Errorf("secrets %v", man.Secrets)
	}
}

// D7: the machine map beats a sibling worktree's reference.
func TestStartEnvMapBeatsSiblingSecretReference(t *testing.T) {
	repo, _, base := siblingRepo(t)
	installFakeDocker(t)
	t.Setenv("SF_TEST_MAPPED_TOKEN", "x")
	mapSecret(t, repo, "tok", "env:SF_TEST_MAPPED_TOKEN")
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	ad := filepath.Join(nw, ".stackfork")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "app.compose.yaml"), []byte("services:\n  app:\n    image: alpine:3.20\n    secrets: [tok]\nsecrets:\n  tok:\n    environment: tok\n"), 0o644)
	if out, e, code := runCLI(t, append(base, "start-env", nw)...); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	whereOut, _, _ := runCLI(t, append(base, "--root", nw, "where")...)
	var w struct{ State string }
	json.Unmarshal([]byte(whereOut), &w)
	m, _ := os.ReadFile(filepath.Join(w.State, "manifest.json"))
	var man struct{ Secrets map[string]string }
	json.Unmarshal(m, &man)
	if man.Secrets["tok"] != "env:SF_TEST_MAPPED_TOKEN" {
		t.Errorf("secrets %v", man.Secrets)
	}
}

func TestStartEnvRedactsSiblingSecretsInPrepareLog(t *testing.T) {
	repo, _, base := siblingRepo(t)
	installFakeDocker(t)
	const val = "sibling-secret-value-4242"
	t.Setenv(sibTokenName, val)
	orig := runPrepare
	runPrepare = func(args []string, o, e io.Writer) (int, error) {
		code, err := orig(args, o, e)
		fmt.Fprintln(e, "prepare saw "+val)
		return code, err
	}
	t.Cleanup(func() { runPrepare = orig })
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	logs, _ := filepath.Glob(filepath.Join(base[1], "*", "runs", "*", "*.log"))
	var all strings.Builder
	for _, p := range logs {
		b, _ := os.ReadFile(p)
		all.Write(b)
	}
	if !strings.Contains(out, "prepare saw") { // an implicit prepare writes to the stream, a plan step to its log
		t.Errorf("prepare output missing: %s", out)
	}
	for _, got := range []string{all.String(), out, e} {
		if strings.Contains(got, val) {
			t.Errorf("secret leaked: %s", got)
		}
	}
}

// A sibling's keychain: or op:// reference is not reused (the machine map
// holds those); the Start says so by name, never by reference (N6).
func TestStartEnvNotesDroppedSiblingStoreReference(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	installFakeDocker(t)
	whereOut, _, _ := runCLI(t, append(base, "--root", sib, "where")...)
	var w struct{ State string }
	json.Unmarshal([]byte(whereOut), &w)
	mp := filepath.Join(w.State, "manifest.json")
	raw, _ := os.ReadFile(mp)
	var man map[string]any
	json.Unmarshal(raw, &man)
	man["secrets"] = map[string]string{"tok": "keychain:sibling-item-canary"}
	raw, _ = json.Marshal(man)
	os.WriteFile(mp, raw, 0o600)
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	ad := filepath.Join(nw, ".stackfork")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "app.compose.yaml"), []byte("services:\n  app:\n    image: alpine:3.20\n"), 0o644)
	out, e, _ := runCLI(t, append(base, "start-env", nw)...)
	if !strings.Contains(out+e, "note: sibling's tok uses a store reference; map it with `stackfork secret map") {
		t.Errorf("no note:\n%s\n%s", out, e)
	}
	if strings.Contains(out+e, "sibling-item-canary") {
		t.Error("the reference item leaked")
	}
}

// A recipe prepare would refuse leaves no copied files behind (info nit).
func TestStartEnvRefusedSiblingRecipeCopiesNothing(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	ad := filepath.Join(sib, ".agent-dev")
	os.WriteFile(filepath.Join(ad, "a&b.yml"), []byte("services: {}\n"), 0o644)
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", sib, "--secret", "tok=env:"+sibTokenName,
		"prepare", "-f", "docker-compose.yml", "-f", ".agent-dev/a&b.yml", "--env-file", ".agent-dev/runtime.env")...); code != 0 {
		t.Fatal(e)
	}
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	_, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code == 0 || !strings.Contains(e, "plain value") {
		t.Fatalf("%d %s", code, e)
	}
	if _, err := os.Stat(filepath.Join(nw, ".agent-dev")); err == nil {
		t.Error("files were copied before the recipe was refused")
	}
}

// A sibling's reference that does not resolve here is not borrowed: the Start
// goes on and says how to map it (B3a).
func TestStartEnvSkipsASiblingReferenceThatDoesNotResolve(t *testing.T) {
	repo, _, base := siblingRepo(t)
	installFakeDocker(t)
	t.Setenv(sibTokenName, "") // the sibling's variable is not set in this shell
	os.Unsetenv(sibTokenName)
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if strings.Contains(out, "reusing secret references") || !strings.Contains(out, "sibling's tok") || !strings.Contains(out, "secret map") {
		t.Errorf("expected a skip note for tok:\n%s", out)
	}
}

// secret forget drops one reference from the instance (B3b).
func TestSecretForgetDropsTheRecordedReference(t *testing.T) {
	_, sib, base := siblingRepo(t)
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", sib, "secret", "forget", "tok")...); code != 0 {
		t.Fatal(e)
	}
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", sib, "secret", "forget", "tok")...); code == 0 || !strings.Contains(e, "no recorded reference tok") {
		t.Errorf("second forget: %d %s", code, e)
	}
	if got := lsSources(t, append(append([]string{}, base...), "--root", sib, "secret", "ls", "--json")...); got["tok"] != "" {
		t.Errorf("tok still listed: %v", got)
	}
}
