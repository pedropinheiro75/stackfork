package repos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pushAhead adds n commits to origin/main from a second clone.
func pushAhead(t *testing.T, origin string, n int) {
	t.Helper()
	clone := filepath.Join(tempRoot(t), "other")
	git(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	for i := 0; i < n; i++ {
		f := filepath.Join(clone, "up"+string(rune('a'+i)))
		os.WriteFile(f, []byte("x"), 0o644)
		git(t, clone, "add", ".")
		git(t, clone, "commit", "-q", "-m", "up")
	}
	git(t, clone, "push", "-q", "origin", "main")
}

func TestSyncUpstream(t *testing.T) {
	t.Run("up to date says nothing", func(t *testing.T) {
		repo, _ := mkWithOrigin(t)
		if msg := SyncUpstream(repo, 10*time.Second); msg != "" {
			t.Fatalf("got %q", msg)
		}
	})
	t.Run("clean and behind fast-forwards", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 2)
		msg := SyncUpstream(repo, 10*time.Second)
		if !strings.Contains(msg, "updated main") || !strings.Contains(msg, "2 new commits") {
			t.Fatalf("got %q", msg)
		}
		if n := strings.TrimSpace(git(t, repo, "rev-list", "--count", "HEAD..origin/main")); n != "0" {
			t.Fatalf("still behind by %s", n)
		}
	})
	t.Run("uncommitted changes: warns and touches nothing", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 3)
		os.WriteFile(filepath.Join(repo, "f"), []byte("local edit"), 0o644)
		head := git(t, repo, "rev-parse", "HEAD")
		msg := SyncUpstream(repo, 10*time.Second)
		for _, want := range []string{"warning:", "3 commits behind origin/main", "uncommitted changes", "pull --rebase"} {
			if !strings.Contains(msg, want) {
				t.Errorf("missing %q in %q", want, msg)
			}
		}
		if git(t, repo, "rev-parse", "HEAD") != head {
			t.Error("HEAD moved")
		}
		if b, _ := os.ReadFile(filepath.Join(repo, "f")); string(b) != "local edit" {
			t.Error("local edit lost")
		}
	})
	t.Run("local commits: warns and touches nothing", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 1)
		os.WriteFile(filepath.Join(repo, "mine"), []byte("m"), 0o644)
		git(t, repo, "add", "mine")
		git(t, repo, "commit", "-q", "-m", "mine")
		head := git(t, repo, "rev-parse", "HEAD")
		msg := SyncUpstream(repo, 10*time.Second)
		if !strings.Contains(msg, "1 commit behind") || !strings.Contains(msg, "1 local commit") {
			t.Fatalf("got %q", msg)
		}
		if git(t, repo, "rev-parse", "HEAD") != head {
			t.Error("HEAD moved")
		}
	})
	t.Run("no upstream says nothing", func(t *testing.T) {
		repo := mkRepo(t, filepath.Join(tempRoot(t), "solo"))
		if msg := SyncUpstream(repo, 10*time.Second); msg != "" {
			t.Fatalf("got %q", msg)
		}
	})
	t.Run("unreachable remote is a note, not an error", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		os.RemoveAll(origin)
		msg := SyncUpstream(repo, 10*time.Second)
		if !strings.Contains(msg, "could not fetch origin/main") {
			t.Fatalf("got %q", msg)
		}
	})
}
