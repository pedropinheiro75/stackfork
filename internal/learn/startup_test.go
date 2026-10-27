package learn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const pkgScripts = `{"scripts":{"migrate":"knex migrate:latest","seed":"node seed.js","dev":"node ."}}`

func rendered(app map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"services": map[string]interface{}{"app": app}}
}

func annotate(t *testing.T, root string, app map[string]interface{}) knowledge.Service {
	t.Helper()
	svcs := []knowledge.Service{{Name: "app", Build: true}}
	annotateStartup(root, rendered(app), []string{"compose.yaml"}, svcs)
	return svcs[0]
}

func TestStartupEntrypointScript(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":  pkgScripts,
		"compose.yaml":  "services:\n  app:\n    build: .\n",
		"Dockerfile":    "FROM node:22\nCOPY entrypoint.sh /usr/local/bin/entrypoint.sh\nENTRYPOINT [\"entrypoint.sh\"]\nCMD [\"node\", \".\"]\n",
		"entrypoint.sh": "#!/bin/sh\nset -e\n# npm run seed is documented here\nnpm run migrate && npm run seed\nexec \"$@\"\n",
	})
	svc := annotate(t, root, map[string]interface{}{"build": map[string]interface{}{"context": root}})
	if svc.StartupMigrate == nil || svc.StartupMigrate.File != "entrypoint.sh" || svc.StartupMigrate.Line != 4 {
		t.Fatalf("migrate = %+v", svc.StartupMigrate)
	}
	if svc.StartupSeed == nil || svc.StartupSeed.Line != 4 {
		t.Fatalf("seed = %+v", svc.StartupSeed)
	}
}

func TestStartupComposeCommand(t *testing.T) {
	root := writeTree(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    build: .\n    command: sh -c \"npm run migrate && npm start\"\n",
	})
	svc := annotate(t, root, map[string]interface{}{"command": []interface{}{"sh", "-c", "npm run migrate && npm start"}})
	if svc.StartupMigrate == nil || svc.StartupMigrate.File != "compose.yaml" || svc.StartupMigrate.Line != 4 {
		t.Fatalf("migrate = %+v", svc.StartupMigrate)
	}
	if svc.StartupSeed != nil {
		t.Fatalf("no seed expected, got %+v", svc.StartupSeed)
	}
}

func TestStartupNegative(t *testing.T) {
	root := writeTree(t, map[string]string{
		"package.json":  pkgScripts,
		"compose.yaml":  "services:\n  app:\n    build: .\n",
		"Dockerfile":    "FROM node:22\nRUN npm run migrate\nENTRYPOINT [\"entrypoint.sh\"]\n",
		"entrypoint.sh": "#!/bin/sh\nexec node .\n",
	})
	svc := annotate(t, root, map[string]interface{}{"build": map[string]interface{}{"context": root}})
	if svc.StartupMigrate != nil || svc.StartupSeed != nil {
		t.Fatalf("a build-time RUN and a plain entrypoint run nothing at start: %+v %+v", svc.StartupMigrate, svc.StartupSeed)
	}
}

func TestProposePlanEntrypointNote(t *testing.T) {
	root := writeTree(t, map[string]string{"package.json": pkgScripts})
	k := &knowledge.Knowledge{}
	k.Facts.Services = []knowledge.Service{{Name: "app", Ports: []string{"3000:3000"}, Build: true,
		StartupMigrate: &knowledge.StartupRun{File: "entrypoint.sh", Line: 4, Cmd: "npm run migrate && npm run seed"},
		StartupSeed:    &knowledge.StartupRun{File: "entrypoint.sh", Line: 4, Cmd: "npm run migrate && npm run seed"}}}
	p := ProposePlan(root, k)
	for _, s := range p.Steps {
		if s.Kind == knowledge.KindExec {
			t.Fatalf("no exec step expected, got %+v", s)
		}
	}
	found := 0
	for _, s := range p.Steps {
		if s.Kind == knowledge.KindNote && strings.Contains(s.Text, "runs in the app's entrypoint (entrypoint.sh:4)") {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("want 2 notes, got %d in %+v", found, p.Steps)
	}
	// Negative: no startup finding keeps the exec steps.
	k.Facts.Services[0].StartupMigrate, k.Facts.Services[0].StartupSeed = nil, nil
	if p := ProposePlan(root, k); p.MigrateStep() == nil || p.SeedStep() == nil {
		t.Fatalf("expected migrate and seed exec steps, got %+v", p.Steps)
	}
}

func lintK() *knowledge.Knowledge {
	k := &knowledge.Knowledge{}
	k.Facts.Services = []knowledge.Service{{Name: "app", Ports: []string{"3000:3000"},
		StartupMigrate: &knowledge.StartupRun{File: "entrypoint.sh", Line: 4, Cmd: "npm run migrate"}}}
	return k
}

func TestStepWarnings(t *testing.T) {
	k := lintK()
	mig := knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: "bun run db:migrate", Once: knowledge.OnceSchema, Service: "app"}
	w := StepWarnings(k, mig)
	if len(w) != 1 || !strings.Contains(w[0], "entrypoint.sh:4") || !strings.Contains(w[0], "--remove migrate") {
		t.Fatalf("got %v", w)
	}
	// A seed that migrates first: double migrate with the entrypoint and the earlier step.
	seed := knowledge.Step{ID: "seed", Kind: knowledge.KindExec, Cmd: "bun run db:migrate && bun run db:seed", Once: knowledge.OnceData}
	k.Plan.Steps = []knowledge.Step{mig, seed}
	w = StepWarnings(k, seed)
	if len(w) != 2 || !strings.Contains(w[1], "runs twice") {
		t.Fatalf("got %v", w)
	}
	// Negative: no finding, separate commands, deps step.
	k.Facts.Services[0].StartupMigrate = nil
	seed.Cmd = "bun run db:seed"
	if w := StepWarnings(k, seed); len(w) != 0 {
		t.Fatalf("got %v", w)
	}
	if w := StepWarnings(k, knowledge.Step{ID: "deps", Kind: knowledge.KindExec, Cmd: "bun install", Once: knowledge.OnceDeps}); len(w) != 0 {
		t.Fatalf("got %v", w)
	}
}
