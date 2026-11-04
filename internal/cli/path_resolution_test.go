package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitWorktree creates a repo with one commit on branch, then a real
// worktree of it (not just a plain subdirectory - git treats those very
// differently: `git -C <subdir> rev-parse --show-toplevel` resolves to the
// *parent* repo's toplevel unless it's an actual worktree), returning the
// worktree's path.
func gitWorktree(t *testing.T, mainRepo, worktreeRelPath, branch string) string {
	t.Helper()
	// An explicit identity: CI runners have no global git user configured.
	if out, err := exec.Command("git", "-C", mainRepo, "-c", "user.email=test@example.com", "-c", "user.name=test",
		"commit", "-q", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	worktreePath := filepath.Join(mainRepo, worktreeRelPath)
	if out, err := exec.Command("git", "-C", mainRepo, "worktree", "add", "-q", "-b", branch, worktreePath, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	return worktreePath
}

// TestPrepareResolvesRelativeFilesAgainstRoot reproduces the real Copilot
// CLI session's exact failing invocation: run from the main checkout,
// `stackfork --root .worktrees/pr630 prepare -f .agent-dev/nuxt.compose.yaml`
// looked for the file relative to the process's own cwd (the main
// checkout) instead of --root (the worktree), and failed with "No source
// configuration found" even though the file genuinely existed under the
// worktree.
func TestPrepareResolvesRelativeFilesAgainstRoot(t *testing.T) {
	requireDocker(t)
	mainRepo := initRepo(t)
	worktree := gitWorktree(t, mainRepo, ".worktrees/pr630", "pr630")

	if err := os.MkdirAll(filepath.Join(worktree, ".agent-dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(worktree, ".agent-dev", "nuxt.compose.yaml"), fixtureCompose)
	// A same-named file under the MAIN checkout too, proving a pass only
	// because cwd-resolution accidentally found something isn't possible -
	// it has to actually be looking under the worktree.
	if err := os.MkdirAll(filepath.Join(mainRepo, ".agent-dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(mainRepo, ".agent-dev", "nuxt.compose.yaml"), "services: {}\n")

	stateDir := t.TempDir()

	// Run from the main checkout's own directory (the process cwd), exactly
	// like the failing session, with a relative --root and a relative -f.
	cmd := exec.Command(stackforkBinary(t),
		"--state-dir", stateDir, "--root", ".worktrees/pr630",
		"prepare", "-f", ".agent-dev/nuxt.compose.yaml")
	cmd.Dir = mainRepo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare failed: %v: %s", err, out)
	}
	if !strings.Contains(string(out), `"result": "prepared`) {
		t.Fatalf("expected a successful prepare using the worktree's own compose file, got: %s", out)
	}
	if !strings.Contains(string(out), `"db"`) {
		t.Fatalf("expected the worktree's fixtureCompose (service \"db\"), not the main checkout's empty one, got: %s", out)
	}
}

// TestPrepareMissingFileErrorNamesAbsolutePath: the refusal must print the
// absolute path(s) it actually looked for, not just a generic message - so
// a misconfigured --root is obvious from the error alone.
func TestPrepareMissingFileErrorNamesAbsolutePath(t *testing.T) {
	mainRepo := initRepo(t)
	worktree := gitWorktree(t, mainRepo, ".worktrees/pr630", "pr630")
	stateDir := t.TempDir()

	cmd := exec.Command(stackforkBinary(t),
		"--state-dir", stateDir, "--root", ".worktrees/pr630",
		"prepare", "-f", ".agent-dev/does-not-exist.yaml")
	cmd.Dir = mainRepo
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected prepare to fail, got: %s", out)
	}
	wantPath := filepath.Join(worktree, ".agent-dev", "does-not-exist.yaml")
	resolvedWant := filepath.Dir(wantPath)
	if wt, err := filepath.EvalSymlinks(worktree); err == nil { // also the long name of a Windows 8.3 temp path
		resolvedWant = filepath.Join(wt, ".agent-dev")
	}
	if !strings.Contains(string(out), resolvedWant) {
		t.Fatalf("expected the error to name the absolute path under the worktree (%s), got: %s", resolvedWant, out)
	}
}
