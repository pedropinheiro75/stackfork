package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// learnedInWorktree is adapterRepo with the learn done in a linked worktree
// instead of the main checkout: the knowledge and the untracked adapter
// files (env file included) live only there. nw is another fresh worktree.
func learnedInWorktree(t *testing.T, planArgs []string, envName string) (repo, w, nw string, base []string) {
	t.Helper()
	repo, nw, base = adapterRepo(t, planArgs)
	w = filepath.Join(repo, ".worktrees", "chore-218")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "chore-218", w).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	for _, p := range []string{".agent-dev", ".stackfork"} {
		if err := os.Rename(filepath.Join(repo, p), filepath.Join(w, p)); err != nil {
			t.Fatal(err)
		}
	}
	if envName != "runtime.env" {
		os.Rename(filepath.Join(w, ".agent-dev", "runtime.env"), filepath.Join(w, ".agent-dev", envName))
		os.WriteFile(filepath.Join(w, ".agent-dev", "compose.yml"), []byte("services:\n  app:\n    env_file: ./.agent-dev/"+envName+"\n"), 0o644)
	}
	k, err := knowledge.Load(filepath.Join(w, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	k.Verification = &knowledge.Verification{Date: "2026-05-01", Confirmation: "works"}
	if err := verifiedFixture(k).Save(filepath.Join(w, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return repo, w, nw, base
}

func TestStartEnvBorrowsVerifiedKnowledgeAndFilesFromAWorktree(t *testing.T) {
	plan := []string{"-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "--env-file", ".agent-dev/learn.env"}
	_, w, nw, base := learnedInWorktree(t, plan, "learn.env")
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	for _, want := range []string{"copied .agent-dev/compose.yml from " + w, "copied .agent-dev/learn.env from " + w} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, f := range []string{"compose.yml", "learn.env"} {
		if _, err := os.Stat(filepath.Join(nw, ".agent-dev", f)); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(nw, ".stackfork", "knowledge.json")); err == nil {
		t.Error("borrowed knowledge was copied")
	}
}

func TestStartEnvRefusesEnvFileOutsideAdapterFoldersNamingFileAndSource(t *testing.T) {
	plan := []string{"-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "--env-file", "learn.env"}
	_, w, nw, base := learnedInWorktree(t, plan, "runtime.env")
	os.WriteFile(filepath.Join(w, "learn.env"), []byte("A=1\n"), 0o600)
	out, e, code := runCLI(t, append(base, "start-env", nw)...)
	if code == 0 || !strings.Contains(out+e, "learn.env") || !strings.Contains(out+e, w) || !strings.Contains(out+e, ".stackfork/runtime.env") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
}
