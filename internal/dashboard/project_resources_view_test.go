package dashboard

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Resources charts (static/project-resources.js) render the api.md fixture:
// series paths, gaps, thresholds, table view, units, escaping and the empty state.
func TestResourcesChartsRender(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	out, err := exec.Command(node, abs("testdata/resources.case.js"), abs("static/project-resources.js"), abs("testdata/resources.fixture.json")).CombinedOutput()
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "ok") {
		t.Fatalf("resources.case.js: %v\n%s", err, out)
	}
}
