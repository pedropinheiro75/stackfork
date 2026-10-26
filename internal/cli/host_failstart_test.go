//go:build !windows

package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

func starter(t *testing.T) func(wait string, step knowledge.Step) (string, error) {
	t.Helper()
	root, stateDir, project := hostEnv(t)
	return func(wait string, step knowledge.Step) (string, error) {
		t.Setenv("STACKFORK_HOST_WAIT_TIMEOUT", wait)
		var out bytes.Buffer
		err := lifecycle.HostStart(&out, &out, []string{"--root", root, "--state-dir", stateDir}, root, filepath.Join(stateDir, project), step)
		return out.String(), err
	}
}

// A plan host step that exits while it starts fails, whatever the wait.
func TestHostStepFailsWhenTheAppExitsAtStart(t *testing.T) {
	start := starter(t)
	for i, wait := range []string{"0", "5s"} {
		name := []string{"api", "api2"}[i]
		_, err := start(wait, knowledge.Step{ID: "api", Kind: knowledge.KindHost, Name: name, Cmd: `sh -c "echo ERR_MODULE_NOT_FOUND; exit 1"`})
		if err == nil || !strings.Contains(err.Error(), "ERR_MODULE_NOT_FOUND") {
			t.Errorf("wait %s: %v", wait, err)
		}
	}
}

// A host step that declares a port is expected to listen: running without
// listening when the wait ends is a failure, with the log tail. A step with
// no port keeps the warning (a worker may never listen).
func TestHostStepThatShouldListenFailsWhenItDoesNot(t *testing.T) {
	step := knowledge.Step{ID: "api", Kind: knowledge.KindHost, Name: "api", Port: freePort(t), Cmd: "sleep 30"}
	start := starter(t)
	out, err := start("1s", step)
	if err == nil || !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("err %v\n%s", err, out)
	}
	step.Port, step.Name, step.ID = 0, "worker", "worker"
	if out, err := start("1s", step); err != nil || !strings.Contains(out, "not listening") {
		t.Fatalf("a step with no port only warns: %v\n%s", err, out)
	}
}
