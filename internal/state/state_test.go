package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirPermissions(t *testing.T) {
	base := t.TempDir()
	dir, err := Dir(base, "sf-app-abc")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("expected 0700, got %o", fi.Mode().Perm())
	}
}

func TestWritePrivatePermissionsAndAtomicity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := WritePrivate(path, map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the final file, no leftover temp files, got %v", entries)
	}

	// Overwriting must replace the content, not append.
	if err := WritePrivate(path, map[string]string{"c": "d"}); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifestRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := m["a"]; has {
		t.Fatalf("expected old content replaced, got %v", m)
	}
	if m["c"] != "d" {
		t.Fatalf("expected new content, got %v", m)
	}
}

func TestReadManifestMissingReturnsNil(t *testing.T) {
	m, err := ReadManifest(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m != nil {
		t.Fatalf("expected nil for a missing manifest, got %+v", m)
	}
}

func TestSHA256File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if sum != want {
		t.Fatalf("got %s want %s", sum, want)
	}
}

func TestLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	l1, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Release()

	done := make(chan struct{})
	go func() {
		l2, err := AcquireLock(path)
		if err != nil {
			t.Error(err)
			close(done)
			return
		}
		l2.Release()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("second lock acquired while the first was still held")
	default:
	}
	l1.Release()
	<-done
}
