package testsupport

import (
	"os"
	"testing"
)

// Main is every test package's TestMain body: it points the config dir at a
// throwaway directory (never the developer's ~/.config/stackfork, whose scan
// roots can hold hundreds of repos) and the shared proxy at a container name
// that cannot exist (never the real stackfork-proxy), then runs the tests.
// A test that wants either sets its own with t.Setenv.
//
//	func TestMain(m *testing.M) { os.Exit(testsupport.Main(m)) }
func Main(m *testing.M) int {
	dir, err := os.MkdirTemp("", "stackfork-test-config-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	os.Setenv("STACKFORK_CONFIG_DIR", dir)
	os.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-isolated")
	return m.Run()
}

// Isolated reports whether Main's isolation is active, for a guard test.
func Isolated() bool {
	return os.Getenv("STACKFORK_CONFIG_DIR") != "" && os.Getenv("STACKFORK_PROXY_NAME") != "" && os.Getenv("STACKFORK_PROXY_NAME") != "stackfork-proxy"
}
