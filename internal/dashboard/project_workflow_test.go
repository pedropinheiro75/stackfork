package dashboard_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Project page workflow canvas renders the fixture graph for both modes:
// dimmed inactive branch, failed overlay, panel, escaping and keyboard order.
func TestWorkflowCanvas(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script, _ := filepath.Abs("testdata/workflow_test.js")
	out, err := exec.Command(node, script).CombinedOutput()
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "ok") {
		t.Fatalf("workflow_test.js: %v\n%s", err, out)
	}
}
