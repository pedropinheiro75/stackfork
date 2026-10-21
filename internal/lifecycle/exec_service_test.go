package lifecycle

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// An exec step with no service that can run it fails naming the stack's
// services and the command that records the right one.
func TestExecStepWithoutServiceSaysHowToFixIt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"db":{},"cache":{}}}`), 0o600)
	r := PlanRunner{InstanceDir: dir}
	_, err := r.exec(io.Discard, knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: "bun run db:migrate"})
	for _, want := range []string{"step migrate cannot run", "names no service", "this stack has: cache, db", "stackfork learn record step --id migrate --kind exec --service SERVICE"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	_, err = r.exec(io.Discard, knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Service: "app", Cmd: "x"})
	if err == nil || !strings.Contains(err.Error(), "its service app is not in this stack") {
		t.Errorf("got %v", err)
	}
}
