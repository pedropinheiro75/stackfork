package learn

import (
	"reflect"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestProposePlanNuxtBun(t *testing.T) {
	root := fixture("nuxt-bun")
	migrate, seed := DetectSeed(root)
	if seed != "bun run db:seed" {
		t.Fatalf("fixture seed = %q", seed)
	}
	k := &knowledge.Knowledge{}
	k.Facts.Services = []knowledge.Service{
		{Name: "app", Build: true, Ports: []string{"3000:3000"}},
		{Name: "db", Ports: []string{"5432:5432"}},
	}
	k.Facts.Scripts = detectScripts(root)
	p := ProposePlan(root, k)
	want := []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{knowledge.ModeContainer}},
		{ID: "up-hybrid", Kind: knowledge.KindComposeUp, Modes: []string{knowledge.ModeHybrid}, Exclude: []string{"app"}},
		{ID: "migrate", Kind: knowledge.KindExec, Cmd: migrate, Once: knowledge.OnceSchema},
		{ID: "seed", Kind: knowledge.KindExec, Cmd: seed, Once: knowledge.OnceData},
		{ID: "app", Kind: knowledge.KindHost, Modes: []string{knowledge.ModeHybrid}, Name: "app", Cmd: "bun run dev", PortEnv: []string{"PORT"}},
	}
	if !reflect.DeepEqual(p.Steps, want) {
		t.Fatalf("steps:\n got %+v\nwant %+v", p.Steps, want)
	}
	if p.Status != knowledge.PlanProposed || p.AppService != "app" {
		t.Fatalf("status/app: %+v", p)
	}
	if !reflect.DeepEqual(p.Modes, []string{knowledge.ModeContainer, knowledge.ModeHybrid}) {
		t.Fatalf("modes: %v", p.Modes)
	}
	if err := knowledge.ValidatePlan(p); err != nil {
		t.Fatal(err)
	}
}

func TestProposePlanNoDevScript(t *testing.T) {
	root := t.TempDir()
	put(t, root, "package.json", `{"scripts":{"db:seed":"x"}}`)
	k := &knowledge.Knowledge{}
	k.Facts.Scripts = detectScripts(root)
	p := ProposePlan(root, k)
	if len(p.HostSteps()) != 0 || !reflect.DeepEqual(p.Modes, []string{knowledge.ModeContainer}) {
		t.Fatalf("unexpected: %+v", p)
	}
	if s := p.SeedStep(); s == nil || s.Cmd != "npm run db:seed" {
		t.Fatalf("seed: %+v", s)
	}
}

func TestProposePlanAdapterPrepare(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".stackfork/dev.compose.yaml", "services: {}\n")
	p := ProposePlan(root, &knowledge.Knowledge{})
	if len(p.Steps) < 1 || p.Steps[0].Kind != knowledge.KindPrepare || !reflect.DeepEqual(p.Steps[0].Args, []string{"-f", ".stackfork/dev.compose.yaml"}) {
		t.Fatalf("steps: %+v", p.Steps)
	}
}

func TestDetectDevPort(t *testing.T) {
	cases := []struct {
		name   string
		files  map[string]string
		script string
		want   int
	}{
		{"flag", nil, "vite --port 5173", 5173},
		{"env", nil, "PORT=4000 node server.js", 4000},
		{"nuxt config", map[string]string{"nuxt.config.ts": "export default defineNuxtConfig({ devServer: { port: 3100 } })"}, "nuxt dev", 3100},
		{"vite config", map[string]string{"vite.config.js": "export default { server: { port: 8081 } }"}, "vite", 8081},
		{"nothing", nil, "node x.js", 0},
	}
	for _, c := range cases {
		root := t.TempDir()
		for n, b := range c.files {
			put(t, root, n, b)
		}
		if got := detectDevPort(root, c.script); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}
