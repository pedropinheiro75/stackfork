package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// adapterRepo is a main checkout with a tracked compose file, untracked
// .agent-dev adapter files and a recorded plan whose prepare step names them,
// plus a fresh linked worktree that has none of the untracked files.
func adapterRepo(t *testing.T, planArgs []string) (repo, nw string, base []string) {
	t.Helper()
	root, state := reposEnv(t)
	repo = scratchRepo(t, filepath.Join(root, "proj"))
	os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte("services:\n  app:\n    image: alpine:3.20\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "compose"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	ad := filepath.Join(repo, ".agent-dev")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "compose.yml"), []byte("services:\n  app:\n    env_file: ./.agent-dev/runtime.env\n"), 0o644)
	os.WriteFile(filepath.Join(ad, "npmrc"), []byte("registry=https://registry.example.test/\n"), 0o600)
	os.WriteFile(filepath.Join(ad, "runtime.env"), []byte("DEV_CHECKOUT="+repo+"\nKEEP=1\n"), 0o600)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: planArgs},
		{ID: "up", Kind: knowledge.KindComposeUp},
	}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	nw = filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	return repo, nw, []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
}

var adapterPlan = []string{"-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "--env-file", ".agent-dev/runtime.env"}

func TestStartEnvCopiesPlanPrepareFilesFromMain(t *testing.T) {
	repo, nw, base := adapterRepo(t, adapterPlan)
	mainEnv, _ := os.ReadFile(filepath.Join(repo, ".agent-dev", "runtime.env"))
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	for _, want := range []string{"copied .agent-dev/compose.yml from the main checkout", "copied .agent-dev/runtime.env from the main checkout", "rewrote DEV_CHECKOUT in .agent-dev/runtime.env"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	env, _ := os.ReadFile(filepath.Join(nw, ".agent-dev", "runtime.env"))
	if !strings.Contains(string(env), "DEV_CHECKOUT="+nw) || !strings.Contains(string(env), "KEEP=1") {
		t.Errorf("worktree runtime.env: %s", env)
	}
	if _, err := os.Stat(filepath.Join(nw, ".agent-dev", "compose.yml")); err != nil {
		t.Error(err)
	}
	if after, _ := os.ReadFile(filepath.Join(repo, ".agent-dev", "runtime.env")); string(after) != string(mainEnv) {
		t.Errorf("main runtime.env changed: %s", after)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".agent-dev", "compose.yml")); !strings.Contains(string(b), "env_file") {
		t.Error("main compose.yml changed")
	}
}

func TestPrepareInWorktreeCopiesNamedFilesFromMain(t *testing.T) {
	_, nw, base := adapterRepo(t, adapterPlan)
	out, e, code := runCLI(t, append(append([]string{}, base...), "--root", nw, "prepare",
		"-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "--env-file", ".agent-dev/runtime.env")...)
	if code != 0 || !strings.Contains(out, "copied .agent-dev/compose.yml from the main checkout") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
}

func TestPrepareRefusesMissingFileTrackedInMain(t *testing.T) {
	repo, nw, base := adapterRepo(t, adapterPlan)
	// tracked in main, absent in the worktree (deleted there)
	os.WriteFile(filepath.Join(repo, "tracked.yml"), []byte("services: {}\n"), 0o644)
	for _, args := range [][]string{{"add", "tracked.yml"}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s", out)
		}
	}
	_, e, code := runCLI(t, append(append([]string{}, base...), "--root", nw, "prepare", "-f", "docker-compose.yml", "-f", "tracked.yml")...)
	if code == 0 || !strings.Contains(e, "tracked.yml is tracked in the main checkout") {
		t.Fatalf("%d %s", code, e)
	}
}

func TestStartEnvNeverOverwritesWorktreeAdapterFile(t *testing.T) {
	_, nw, base := adapterRepo(t, adapterPlan)
	ad := filepath.Join(nw, ".agent-dev")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "compose.yml"), []byte("services:\n  app:\n    image: alpine:3.20 # mine\n"), 0o644)
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if b, _ := os.ReadFile(filepath.Join(ad, "compose.yml")); !strings.Contains(string(b), "# mine") {
		t.Errorf("overwritten: %s", b)
	}
	if strings.Contains(out, "copied .agent-dev/compose.yml") {
		t.Errorf("reported a copy of an existing file:\n%s", out)
	}
}
