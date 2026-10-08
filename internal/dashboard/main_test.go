package dashboard_test

import (
	"os"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"

	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// TestMain isolates the developer's real config dir and proxy from every test.
func TestMain(m *testing.M) {
	dashboard.IndexWait = time.Minute // tests render the full page; the loading state has its own test
	os.Exit(testsupport.Main(m))
}

func TestRealConfigAndProxyAreIsolated(t *testing.T) {
	if !testsupport.Isolated() {
		t.Fatal("tests would read the real config or touch the real proxy")
	}
}
