//go:build darwin

package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a case-insensitive volume, two spellings of one folder resolve to the
// spelling on disk, so a scan root typed in lower case still matches the
// instances recorded under the real name.
func TestResolvePathUsesTheSpellingOnDisk(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(base, "Work", "Repo")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	lower := filepath.Join(base, "work", "repo")
	if _, err := os.Stat(lower); err != nil {
		t.Skip("case-sensitive volume")
	}
	got, err := ResolvePath(lower)
	if err != nil || got != real {
		t.Fatalf("got %q %v, want %q", got, err, real)
	}
	// a path that does not exist keeps its spelling
	missing := filepath.Join(base, "work", "nope")
	if got, _ := ResolvePath(missing); !strings.HasSuffix(got, "work/nope") {
		t.Fatalf("missing path changed: %q", got)
	}
}
