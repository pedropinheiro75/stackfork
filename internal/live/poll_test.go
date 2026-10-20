package live

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newPoller(t *testing.T, root string, entries []Entry) *Poller {
	t.Helper()
	p := NewPoller(root, entries, nil)
	p.Poll()
	p.SetBaseline(p.Digests())
	return p
}

func entry(root, rel, cat string) Entry {
	return Entry{Path: filepath.Join(root, filepath.FromSlash(rel)), Rel: rel, Category: cat}
}

func TestPollUnchangedRewriteIsNoChange(t *testing.T) {
	root := tmp(t)
	f := write(t, root, "Dockerfile", "FROM scratch")
	p := newPoller(t, root, []Entry{entry(root, "Dockerfile", CatBuild)})
	// same bytes, new modification time
	future := time.Now().Add(time.Hour)
	os.WriteFile(f, []byte("FROM scratch"), 0o644)
	os.Chtimes(f, future, future)
	if ch := p.Poll(); len(ch) != 0 {
		t.Fatalf("got %v", ch)
	}
}

func TestPollContentChange(t *testing.T) {
	root := tmp(t)
	f := write(t, root, "bun.lock", "a")
	p := newPoller(t, root, []Entry{entry(root, "bun.lock", CatBuild)})
	os.WriteFile(f, []byte("bb"), 0o644) // size differs, so the stat check sees it
	ch := p.Poll()
	if len(ch) != 1 || ch[0].Path != "bun.lock" || ch[0].Kind != "changed" || ch[0].Category != CatBuild {
		t.Fatalf("got %v", ch)
	}
	// still pending until the baseline moves
	if len(p.Poll()) != 1 {
		t.Fatal("pending change disappeared")
	}
	p.SetBaseline(p.Digests())
	if len(p.Poll()) != 0 {
		t.Fatal("change reported after the baseline moved")
	}
}

func TestPollNewFileInMigrationDir(t *testing.T) {
	root := tmp(t)
	write(t, root, "prisma/migrations/001/migration.sql", "a")
	p := newPoller(t, root, []Entry{entry(root, "prisma/migrations", CatMigrations)})
	write(t, root, "prisma/migrations/002_add_invoices/migration.sql", "b") // depth 2
	ch := p.Poll()
	if len(ch) != 1 || ch[0].Kind != "added" || ch[0].Category != CatMigrations || ch[0].Path != "prisma/migrations/002_add_invoices/migration.sql" {
		t.Fatalf("got %v", ch)
	}
	// depth 3 is out of range
	write(t, root, "prisma/migrations/002_add_invoices/deep/x.sql", "c")
	if len(p.Poll()) != 1 {
		t.Fatal("a file at depth 3 was watched")
	}
}

func TestPollRemovedFile(t *testing.T) {
	root := tmp(t)
	f := write(t, root, "db/migrate/1.sql", "a")
	write(t, root, "db/migrate/2.sql", "b")
	p := newPoller(t, root, []Entry{entry(root, "db/migrate", CatMigrations)})
	os.Remove(f)
	ch := p.Poll()
	if len(ch) != 1 || ch[0].Kind != "removed" || ch[0].Path != "db/migrate/1.sql" || ch[0].Category != CatMigrations {
		t.Fatalf("got %v", ch)
	}
	// a removed single-file entry too
	g := write(t, root, "Dockerfile", "x")
	p = newPoller(t, root, []Entry{entry(root, "Dockerfile", CatBuild)})
	os.Remove(g)
	if ch := p.Poll(); len(ch) != 1 || ch[0].Kind != "removed" {
		t.Fatalf("got %v", ch)
	}
}

func TestPollReadErrorIsNoChange(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("cannot make a file unreadable here")
	}
	root := tmp(t)
	f := write(t, root, "bun.lock", "a")
	p := newPoller(t, root, []Entry{entry(root, "bun.lock", CatBuild)})
	os.WriteFile(f, []byte("changed!"), 0o644)
	os.Chmod(f, 0)
	defer os.Chmod(f, 0o644)
	if ch := p.Poll(); len(ch) != 0 {
		t.Fatalf("read error reported as %v", ch)
	}
	os.Chmod(f, 0o644)
	if ch := p.Poll(); len(ch) != 1 {
		t.Fatalf("retry did not see the change: %v", ch)
	}
}

func TestReadBranch(t *testing.T) {
	repo := tmp(t)
	git(t, repo, "init", "-q", "-b", "main")
	write(t, repo, "f", "x")
	git(t, repo, "add", "f")
	git(t, repo, "commit", "-q", "-m", "init")
	if b, det, err := ReadBranch(repo); err != nil || det || b != "main" {
		t.Fatalf("main checkout: %q %v %v", b, det, err)
	}
	wt := filepath.Join(tmp(t), "feature-login")
	git(t, repo, "worktree", "add", "-q", "-b", "feature/login", wt)
	if b, det, err := ReadBranch(wt); err != nil || det || b != "feature/login" {
		t.Fatalf("worktree: %q %v %v", b, det, err)
	}
	git(t, wt, "checkout", "-q", "--detach")
	if b, det, err := ReadBranch(wt); err != nil || !det || b != "" {
		t.Fatalf("detached: %q %v %v", b, det, err)
	}
	git(t, wt, "checkout", "-q", "-b", "other")
	if b, _, _ := ReadBranch(wt); b != "other" {
		t.Fatalf("switched: %q", b)
	}
}
