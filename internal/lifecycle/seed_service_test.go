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
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"a":{"build":"."},"b":{}}}`), 0o600)
	var out, errb bytes.Buffer
	err := RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "echo hi", "svc", knowledge.ModeContainer, knowledge.Plan{}, nil, nil)
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

func TestRunSeedSuggestsTheHostWhenNoServiceRunsTheApp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"db":{"image":"postgres:17"}}}`), 0o600)
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "bun run db:seed", Service: "web"}}}
	var out, errb bytes.Buffer
	err := RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "bun run db:seed", "web", knowledge.ModeContainer, plan, nil, nil)
	want := `stackfork learn record step --id seed --kind exec --on host --cmd "bun run db:seed" --once data`
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(errb.String(), want) {
		t.Fatalf("%v\n%s", err, errb.String())
	}
	// no service resolves at all
	errb.Reset()
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"db":{"image":"postgres:17"},"cache":{"image":"redis:7"}}}`), 0o600)
	err = RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "bun run db:seed", "", knowledge.ModeContainer, plan, nil, nil)
	if err == nil || !strings.Contains(errb.String(), "--on host") {
		t.Fatalf("%v\n%s", err, errb.String())
	}
}
