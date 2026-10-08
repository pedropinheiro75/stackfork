package dashboard_test

import (
	"os"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// TestMain isolates the developer's real config dir and proxy from every test.
func TestMain(m *testing.M) { os.Exit(testsupport.Main(m)) }

func TestRealConfigAndProxyAreIsolated(t *testing.T) {
	if !testsupport.Isolated() {
		t.Fatal("tests would read the real config or touch the real proxy")
	}
}
