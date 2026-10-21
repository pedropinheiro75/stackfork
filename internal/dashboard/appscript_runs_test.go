package dashboard_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runAppJS loads static/app.js in Node (a stub document) and runs one case
// file from testdata; the case asserts on the functions app.js exports.
func runAppJS(t *testing.T, caseFile string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	out, err := exec.Command(node, abs("testdata/appjs_harness.js"), abs("static/app.js"), abs(filepath.Join("testdata", caseFile))).CombinedOutput()
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "ok") {
		t.Fatalf("%s: %v\n%s", caseFile, err, out)
	}
}

// The live run in the plan list is written onto the rows and written again
// after every render (design D13).
func TestPlanListLiveRunSurvivesARender(t *testing.T) {
	runAppJS(t, "runs_live.case.js")
}

// The LogDrawer's Run source opens on the failed step and lists every step
// with a chip (design D14).
func TestRunLogDrawerStepSelector(t *testing.T) {
	runAppJS(t, "runs_drawer.case.js")
}

// A click inside the run callout (Retry, View log, the body) must not reach
// the document's [data-run] handler, which posts /api/run.
func TestRunCalloutIsNotAClickTarget(t *testing.T) {
	runAppJS(t, "run_callout.case.js")
}

// A finished Start removes only its own progress text, and a card's Retry
// stays disabled while its Start is in flight.
func TestStartsKeepOwnProgressAndDisabledRetry(t *testing.T) {
	runAppJS(t, "starts.case.js")
}

// A Check or Set value in flight keeps its busy state across a render, and
// the log gate is recognised by its header only (design D14, D11).
func TestSecretsButtonsStayBusyAcrossARender(t *testing.T) {
	runAppJS(t, "secrets.case.js")
}

// A reset streams, shows its progress and fails like a Start, but is not one.
func TestResetStreamsLikeStart(t *testing.T) {
	runAppJS(t, "reset.case.js")
}
