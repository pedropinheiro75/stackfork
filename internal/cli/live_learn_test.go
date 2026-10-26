package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveLearnReusePath covers task 13.9: learn in one checkout, commit
// .stackfork/, then a second worktree of the same repo applies the adapter
// and recipe directly - `up` never needs `learn` run again, and the
// committed knowledge is readable as `verified` with no re-scan.
func TestLiveLearnReusePath(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	modRoot := moduleRoot(t)

	before := snapshotDocker(t)
	scratch := t.TempDir()
	repo1 := filepath.Join(scratch, "app")
	if err := os.MkdirAll(repo1, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "-q", "-b", "main", repo1)
	mustRun(t, "git", "-C", repo1, "config", "user.email", "test@example.com")
	mustRun(t, "git", "-C", repo1, "config", "user.name", "test")
	mustRun(t, "git", "-C", repo1, "config", "commit.gpgsign", "false")
	data, err := os.ReadFile(filepath.Join(modRoot, "testdata", "pg17-fixed-name", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo1, "compose.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repo1, "add", "-A")
	mustRun(t, "git", "-C", repo1, "commit", "-q", "-m", "init")

	state := filepath.Join(scratch, "state")
	legacyState := filepath.Join(scratch, "legacy-state")
	sf := func(root string, args ...string) string {
		return mustRun(t, bin, append([]string{"--root", root, "--state-dir", state, "--legacy-state-dir", legacyState}, args...)...)
	}

	// Worktree A: scan, record a recipe, verify.
	sf(repo1, "learn")
	sf(repo1, "learn", "record", "recipe", "--prepare", "stackfork prepare -f compose.yaml", "--up", "stackfork up")
	sf(repo1, "learn", "record", "entry", "--none")
	sf(repo1, "prepare", "-f", "compose.yaml")
	sf(repo1, "learn", "verify", "--check", "db up", "--confirm", "yes, pg_isready passes")

	mustRun(t, "git", "-C", repo1, "add", ".stackfork")
	mustRun(t, "git", "-C", repo1, "commit", "-q", "-m", "learn: verified knowledge")

	// Worktree B: a fresh checkout of the same commit. `up` must succeed
	// with no call to `learn` in between - the adapter (compose.yaml) is
	// already committed and runnable on its own; knowledge.json is never a
	// runtime dependency of prepare/up, only of what an agent chooses to
	// read first.
	repo2 := filepath.Join(scratch, "app-worktree-b")
	mustRun(t, "git", "-C", repo1, "worktree", "add", "-q", "--detach", repo2, "main")

	if _, err := os.Stat(filepath.Join(repo2, ".stackfork", "knowledge.json")); err != nil {
		t.Fatalf("expected the committed .stackfork/knowledge.json to be present in the new worktree: %v", err)
	}
	whereOut := sf(repo2, "where")
	if !strings.Contains(whereOut, `"status": "verified"`) {
		t.Fatalf("expected where to report the committed knowledge as verified with no re-scan:\n%s", whereOut)
	}

	sf(repo2, "prepare", "-f", filepath.Join(repo2, "compose.yaml"))
	sf(repo2, "up", "--wait-timeout", "90")
	t.Log("PASS: worktree B applied the committed adapter and started with no learn call")

	sf(repo2, "down", "--volumes")
	mustRun(t, "git", "-C", repo1, "worktree", "remove", "--force", repo2)

	after := snapshotDocker(t)
	assertSameInventory(t, before, after)
}
