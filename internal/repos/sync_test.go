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

func TestSyncResult(t *testing.T) {
	d := 10 * time.Second
	t.Run("clean behind: updated", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 2)
		r := Sync(repo, d, true)
		if !r.Updated || r.Behind != 2 || r.Ahead != 0 || r.Dirty || r.FetchFailed || r.Upstream != "origin/main" ||
			r.UpstreamSHA != strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")) || !strings.Contains(r.Line, "updated main") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("up to date", func(t *testing.T) {
		repo, _ := mkWithOrigin(t)
		if r := Sync(repo, d, true); r.Updated || r.Behind != 0 || r.Line != "" || r.Upstream != "origin/main" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("dirty", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 3)
		os.WriteFile(filepath.Join(repo, "f"), []byte("edit"), 0o644)
		r := Sync(repo, d, true)
		if r.Updated || !r.Dirty || r.Behind != 3 || r.Ahead != 0 || !strings.HasPrefix(r.Line, "warning:") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("local commits", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 1)
		os.WriteFile(filepath.Join(repo, "mine"), []byte("m"), 0o644)
		git(t, repo, "add", "mine")
		git(t, repo, "commit", "-q", "-m", "mine")
		r := Sync(repo, d, true)
		if r.Updated || r.Dirty || r.Behind != 1 || r.Ahead != 1 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("no upstream", func(t *testing.T) {
		repo := mkRepo(t, filepath.Join(tempRoot(t), "solo"))
		if r := Sync(repo, d, true); r != (SyncResult{}) {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("fetch failed", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		os.RemoveAll(origin)
		if r := Sync(repo, d, true); !r.FetchFailed || r.Updated || r.Upstream != "origin/main" {
			t.Fatalf("%+v", r)
		}
	})
}

func TestSyncBlocked(t *testing.T) {
	t.Run("clean and behind is not blocked; a check does not merge", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 1)
		head := git(t, repo, "rev-parse", "HEAD")
		if r := Sync(repo, 10*time.Second, false); r.Blocked || r.Line != "" {
			t.Fatalf("got %+v", r)
		}
		if git(t, repo, "rev-parse", "HEAD") != head {
			t.Error("a check moved HEAD")
		}
		if r := Sync(repo, 10*time.Second, true); r.Blocked || !strings.Contains(r.Line, "updated main") {
			t.Fatalf("got %+v", r)
		}
	})
	t.Run("behind with local changes is blocked", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 1)
		os.WriteFile(filepath.Join(repo, "f"), []byte("local edit"), 0o644)
		for _, apply := range []bool{false, true} {
			if r := Sync(repo, 10*time.Second, apply); !r.Blocked || !strings.Contains(r.Line, "warning:") {
				t.Fatalf("apply=%v: %+v", apply, r)
			}
		}
	})
	t.Run("up to date, no upstream and unreachable are not blocked", func(t *testing.T) {
		repo, origin := mkWithOrigin(t)
		if r := Sync(repo, 10*time.Second, true); r.Blocked {
			t.Fatalf("%+v", r)
		}
		os.RemoveAll(origin)
		if r := Sync(repo, 10*time.Second, true); r.Blocked || r.Line == "" {
			t.Fatalf("%+v", r)
		}
	})
}

// `merge --ff-only` refuses to overwrite an untracked file but silently
// overwrites an ignored one. Unattended (live mode) must not.
func TestSyncUnattendedBlocksWhenIncomingFilesWouldOverwriteLocalOnes(t *testing.T) {
	setup := func(t *testing.T) (repo string) {
		repo, origin := mkWithOrigin(t)
		pushAhead(t, origin, 1) // origin/main adds "upa"
		os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("/upa\n"), 0o644)
		os.WriteFile(filepath.Join(repo, "upa"), []byte("my local secret"), 0o644)
		return repo
	}
	repo := setup(t)
	head := git(t, repo, "rev-parse", "HEAD")
	res := SyncUnattended(repo, 10*time.Second)
	if res.Updated || !res.Blocked || res.Behind != 1 || !strings.Contains(res.Line, "upa") || !strings.Contains(res.Line, "overwritten") {
		t.Fatalf("%+v", res)
	}
	if git(t, repo, "rev-parse", "HEAD") != head {
		t.Error("HEAD moved")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "upa")); string(b) != "my local secret" {
		t.Errorf("the ignored file was overwritten: %q", b)
	}
	// an ignored FILE where upstream adds a file under that name as a directory
	repoD, originD := mkWithOrigin(t)
	cloneD := filepath.Join(tempRoot(t), "dir")
	git(t, filepath.Dir(cloneD), "clone", "-q", originD, cloneD)
	os.MkdirAll(filepath.Join(cloneD, "conf"), 0o755)
	os.WriteFile(filepath.Join(cloneD, "conf", "app.yml"), []byte("x"), 0o644)
	git(t, cloneD, "add", ".")
	git(t, cloneD, "commit", "-q", "-m", "conf dir")
	git(t, cloneD, "push", "-q", "origin", "main")
	os.WriteFile(filepath.Join(repoD, ".git", "info", "exclude"), []byte("/conf\n"), 0o644)
	os.WriteFile(filepath.Join(repoD, "conf"), []byte("my local file"), 0o644)
	if res := SyncUnattended(repoD, 10*time.Second); res.Updated || !res.Blocked || !strings.Contains(res.Line, "conf") {
		t.Fatalf("parent file: %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(repoD, "conf")); string(b) != "my local file" {
		t.Errorf("the ignored file was lost: %q", b)
	}
	// without a collision the unattended sync fast-forwards as before
	repo2, origin2 := mkWithOrigin(t)
	pushAhead(t, origin2, 1)
	if res := SyncUnattended(repo2, 10*time.Second); !res.Updated || res.Blocked {
		t.Fatalf("no collision: %+v", res)
	}
}
