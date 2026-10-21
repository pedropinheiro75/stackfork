package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// TestMain isolates the developer's real config dir and proxy from every test.
func TestMain(m *testing.M) {
	code := testsupport.Main(m)
	// The tests that need a real binary build it into a stackfork-bin-* temp
	// dir (about 15 MB); remove it so runs do not pile up in $TMPDIR.
	for _, bin := range []string{builtPath, liveBinaryPath} {
		if dir := filepath.Dir(bin); strings.HasPrefix(filepath.Base(dir), "stackfork-bin-") {
			os.RemoveAll(dir)
		}
	}
	os.Exit(code)
}

func TestRealConfigAndProxyAreIsolated(t *testing.T) {
	if !testsupport.Isolated() {
		t.Fatal("tests would read the real config or touch the real proxy")
	}
}

// A test that prepares without --state-dir must never write the real state.
func TestDefaultStateIsIsolated(t *testing.T) {
	dir, err := identity.StateDir()
	home, _ := os.UserHomeDir()
	if err != nil || strings.HasPrefix(dir, filepath.Join(home, ".local")) {
		t.Fatalf("tests would write the real state: %s %v", dir, err)
	}
}
