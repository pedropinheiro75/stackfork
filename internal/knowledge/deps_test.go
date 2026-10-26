package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDepsHashStaysInTheCheckoutAndReadsRegularFilesOnly(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside-"+filepath.Base(root))
	os.MkdirAll(outside, 0o755)
	t.Cleanup(func() { os.RemoveAll(outside) })
	empty := DepsHash(root, "")
	os.WriteFile(filepath.Join(outside, "yarn.lock"), []byte("x"), 0o644)
	if DepsHash(root, "../"+filepath.Base(outside)) != empty {
		t.Fatal("a dir outside the checkout must not be hashed")
	}
	if err := os.Symlink("/dev/zero", filepath.Join(root, "package-lock.json")); err != nil {
		t.Skip(err)
	}
	if DepsHash(root, "") != empty {
		t.Fatal("a symlinked lockfile must be skipped")
	}
}

func TestUsesSelfPort(t *testing.T) {
	if !UsesSelfPort([]string{"vite", "--port", "{{port}}"}) || !UsesSelfPort([]string{"env", "X={{url}}"}) || UsesSelfPort([]string{"env", "X={{api.url}}"}) {
		t.Fatal("UsesSelfPort")
	}
}
