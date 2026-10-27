package learn

import (
	"os"
	"os/exec"
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

func reloadOf(t *testing.T, root string) []knowledge.Reload {
	t.Helper()
	k, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	return k.Reload
}

func TestReloadInference(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	cases := []struct {
		name  string
		files map[string]string
		want  map[string]string
	}{
		{"host vite dev is hot", map[string]string{
			"package.json": "{\n  \"scripts\": {\n    \"dev\": \"vite\"\n  }\n}\n",
			"compose.yaml": "services:\n  app:\n    image: nginx\n    ports: [\"8080:80\"]\n",
		}, map[string]string{"app": "hot"}},
		{"bind mount plus nodemon is hot", map[string]string{
			"compose.yaml": "services:\n  web:\n    image: node:22\n    command: npx nodemon server.js\n    volumes:\n      - .:/app\n    ports: [\"3000:3000\"]\n",
		}, map[string]string{"web": "hot"}},
		{"image COPY only is rebuild", map[string]string{
			"compose.yaml": "services:\n  web:\n    build: .\n    ports: [\"3000:3000\"]\n",
			"Dockerfile":   "FROM node:22\nCOPY . /app\nCMD [\"node\", \"server.js\"]\n",
		}, map[string]string{"web": "rebuild"}},
		{"mounted plain node is restart", map[string]string{
			"compose.yaml": "services:\n  web:\n    image: node:22\n    command: node server.js\n    volumes:\n      - .:/app\n    ports: [\"3000:3000\"]\n  db:\n    image: postgres:16\n",
		}, map[string]string{"web": "restart"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]knowledge.Reload{}
			for _, r := range reloadOf(t, writeTree(t, c.files)) {
				got[r.Service] = r
			}
			for svc, mode := range c.want {
				if r := got[svc]; r.Mode != mode || !r.Proposed || r.Evidence == "" {
					t.Errorf("%s = %+v, want proposed %s with evidence", svc, r, mode)
				}
			}
			if _, ok := got["db"]; ok {
				t.Errorf("a database image has no reload entry: %+v", got["db"])
			}
		})
	}
}

func TestHostReload(t *testing.T) {
	root := writeTree(t, map[string]string{"package.json": "{\n  \"scripts\": {\n    \"dev\": \"vite\",\n    \"start\": \"node server.js\"\n  }\n}\n"})
	k := &knowledge.Knowledge{
		Facts: knowledge.Facts{Scripts: []knowledge.Script{{Role: "dev", Name: "dev", Source: "package.json", Command: "vite"}, {Name: "start", Source: "package.json", Command: "node server.js"}}},
		Plan: knowledge.Plan{Steps: []knowledge.Step{
			{ID: "app", Kind: knowledge.KindHost, Name: "app", Cmd: "npm run dev"},
			{ID: "api", Kind: knowledge.KindHost, Name: "api", Cmd: "npm start"},
		}},
	}
	got := proposeHostReload(root, k)
	if len(got) != 2 || got[0].Mode != "hot" || got[0].Evidence != "package.json:3" || got[1].Mode != "restart" {
		t.Fatalf("got %+v", got)
	}
}
