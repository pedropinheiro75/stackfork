package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// TestMain isolates the developer's real config dir and proxy from every test.
// tempBinDirs are the stackfork-bin-* dirs the tests that need a real binary
// built (about 15 MB each); TestMain removes them so runs do not pile up in $TMPDIR.
var (
	tempBinMu   sync.Mutex
	tempBinDirs []string
)

func trackTempBin(dir string) {
	tempBinMu.Lock()
	defer tempBinMu.Unlock()
	tempBinDirs = append(tempBinDirs, dir)
}

func TestMain(m *testing.M) {
	code := testsupport.Main(m)
	for _, dir := range tempBinDirs {
		os.RemoveAll(dir)
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
