package lifecycle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestRunSeedNamesServicesWhenItsServiceIsNotInTheStack(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"a":{},"b":{}}}`), 0o600)
	var out, errb bytes.Buffer
	err := RunSeed(&out, &errb, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "echo hi", "svc", knowledge.Plan{}, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"seed cannot run: its service svc is not in this stack (this stack has: a, b)", "stackfork learn record step --id seed --kind exec --once data --service SERVICE --cmd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if !strings.Contains(errb.String(), "seed cannot run") {
		t.Errorf("stderr: %s", errb.String())
	}
}
