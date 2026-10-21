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
		{ID: "migrate", Kind: knowledge.KindExec, Modes: []string{knowledge.ModeContainer}, Cmd: migrate, Once: knowledge.OnceSchema},
		{ID: "seed", Kind: knowledge.KindExec, Modes: []string{knowledge.ModeContainer}, Cmd: seed, Once: knowledge.OnceData},
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
		{"next config", map[string]string{"next.config.js": "module.exports = { devServer: { port: 3200 } }"}, "next dev", 3200},
		{"out of range", nil, "vite --port 99999", 0},
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

func appServices() *knowledge.Knowledge {
	k := &knowledge.Knowledge{}
	k.Facts.Services = []knowledge.Service{{Name: "web", Build: true, Ports: []string{"3000:3000/tcp"}}}
	return k
}

func hostPlan(t *testing.T, pkg string, k *knowledge.Knowledge) knowledge.Plan {
	root := t.TempDir()
	put(t, root, "package.json", pkg)
	k.Facts.Scripts = detectScripts(root)
	return ProposePlan(root, k)
}

func TestProposePlanStartOnlyIsContainerOnly(t *testing.T) {
	p := hostPlan(t, `{"scripts":{"start":"node server.js","prestart":"x","start:prod":"y"}}`, appServices())
	if len(p.HostSteps()) != 0 || !reflect.DeepEqual(p.Modes, []string{knowledge.ModeContainer}) {
		t.Fatalf("unexpected: %+v", p)
	}
}

func TestProposePlanDevPick(t *testing.T) {
	p := hostPlan(t, `{"scripts":{"prestart":"x","dev:web":"vite --port 5173"}}`, appServices())
	h := p.HostSteps()
	if len(h) != 1 || h[0].Cmd != "npm run dev:web" || h[0].Port != 5173 {
		t.Fatalf("host: %+v", h)
	}
	p = hostPlan(t, `{"scripts":{"dev:web":"a","start:dev":"b"}}`, appServices())
	if h := p.HostSteps(); len(h) != 1 || h[0].Cmd != "npm run start:dev" {
		t.Fatalf("host: %+v", h)
	}
}

func TestProposePlanDevWithoutAppService(t *testing.T) {
	p := hostPlan(t, `{"scripts":{"dev":"vite"}}`, &knowledge.Knowledge{})
	if len(p.HostSteps()) != 0 || !reflect.DeepEqual(p.Modes, []string{knowledge.ModeContainer}) {
		t.Fatalf("unexpected: %+v", p)
	}
	for _, s := range p.Steps {
		if s.ID == "up-hybrid" {
			t.Fatal("no hybrid compose-up without an app service")
		}
	}
}

// entry-links task 3.1: the scan proposes the heuristic's service and target port.
func TestProposeEntry(t *testing.T) {
	k := &knowledge.Knowledge{}
	if ProposeEntry(k) != nil {
		t.Fatal("no services, no proposal")
	}
	k.Facts.Services = []knowledge.Service{
		{Name: "db", Ports: []string{"5432:5432"}},
		{Name: "web", Ports: []string{"127.0.0.1:8000:3000/tcp"}},
	}
	want := &knowledge.Entry{Service: "web", Port: 3000, Proposed: true}
	if got := ProposeEntry(k); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}
