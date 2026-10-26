package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func onFixture(t *testing.T) (root, path string) {
	t.Helper()
	root = initRepo(t)
	path = filepath.Join(root, ".stackfork", "knowledge.json")
	if err := (&knowledge.Knowledge{Schema: knowledge.CurrentSchema}).Save(path); err != nil {
		t.Fatal(err)
	}
	return
}

func recordedStep(t *testing.T, path, id string) knowledge.Step {
	t.Helper()
	k, err := knowledge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range k.Plan.Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %s", id)
	return knowledge.Step{}
}

func TestLearnRecordStepOn(t *testing.T) {
	root, path := onFixture(t)
	rec := func(args ...string) (string, int) {
		_, e, code := runCLI(t, append([]string{"--root", root, "learn", "record", "step"}, args...)...)
		return e, code
	}
	if e, code := rec("--id", "seed", "--kind", "exec", "--on", "host", "--once", "data", "--cmd", "bun run db:seed", "--dir", "apps/api"); code != 0 {
		t.Fatal(e)
	}
	if s := recordedStep(t, path, "seed"); s.On != knowledge.OnHost || s.Once != knowledge.OnceData || s.Dir != "apps/api" {
		t.Fatalf("%+v", s)
	}
	if e, code := rec("--id", "seed", "--kind", "exec", "--on", "container", "--once", "data", "--cmd", "bun run db:seed", "--service", "web"); code != 0 {
		t.Fatal(e)
	}
	if s := recordedStep(t, path, "seed"); s.On != "" || s.Service != "web" {
		t.Fatalf("--on container must clear on: %+v", s)
	}
	before, _ := os.ReadFile(path)
	for name, args := range map[string][]string{
		"host with kind host":  {"--id", "app", "--kind", "host", "--on", "host", "--cmd", "bun run dev"},
		"container, kind host": {"--id", "app", "--kind", "host", "--on", "container", "--cmd", "bun run dev"},
		"host with service":    {"--id", "seed", "--kind", "exec", "--on", "host", "--service", "web", "--cmd", "bun run db:seed"},
		"unknown value":        {"--id", "seed", "--kind", "exec", "--on", "cloud", "--cmd", "x"},
		"shell syntax":         {"--id", "m", "--kind", "exec", "--on", "host", "--cmd", "a && b"},
	} {
		e, code := rec(args...)
		if code == 0 || !strings.Contains(e, "--on") && !strings.Contains(e, "host") {
			t.Errorf("%s: code %d %s", name, code, e)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatalf("%s: the file changed", name)
		}
	}
	if e, _ := rec("--id", "seed", "--kind", "exec", "--on", "host", "--service", "web", "--cmd", "x"); !strings.Contains(e, "remove --service") {
		t.Fatalf("service message: %s", e)
	}
}

func TestLearnRecordStepHostModeAddsHybridToPlanModes(t *testing.T) {
	root, path := onFixture(t)
	k, _ := knowledge.Load(path)
	k.Plan.Modes = []string{knowledge.ModeContainer}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	_, e, code := runCLI(t, "--root", root, "learn", "record", "step", "--id", "app", "--kind", "host", "--name", "app", "--modes", "hybrid", "--cmd", "node server.js")
	if code != 0 {
		t.Fatal(e)
	}
	k, _ = knowledge.Load(path)
	if !k.Plan.SupportsMode(knowledge.ModeHybrid) || !k.Plan.SupportsMode(knowledge.ModeContainer) {
		t.Fatalf("modes %v", k.Plan.Modes)
	}
}
