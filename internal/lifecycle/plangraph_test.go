package lifecycle

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func hybridPlan() knowledge.Plan {
	c, h := []string{knowledge.ModeContainer}, []string{knowledge.ModeHybrid}
	return knowledge.Plan{Status: knowledge.PlanRecorded, AppService: "app", Steps: []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"--reassign-ports"}},
		{ID: "up", Kind: knowledge.KindComposeUp, Modes: c},
		{ID: "up-deps", Kind: knowledge.KindComposeUp, Modes: h, Exclude: []string{"app", "nginx"}},
		{ID: "migrate", Kind: knowledge.KindExec, Modes: c, Once: knowledge.OnceSchema, Service: "app", Cmd: "npm run db:migrate"},
		{ID: "migrate-host", Kind: knowledge.KindExec, Modes: h, Once: knowledge.OnceSchema, On: knowledge.OnHost, Cmd: "npm run db:migrate"},
		{ID: "seed", Kind: knowledge.KindExec, Modes: c, Once: knowledge.OnceData, Service: "app", Cmd: "npm run db:seed"},
		{ID: "seed-host", Kind: knowledge.KindExec, Modes: h, Once: knowledge.OnceData, On: knowledge.OnHost, Cmd: "npm run db:seed"},
		{ID: "api", Kind: knowledge.KindHost, Modes: h, Name: "api", Cmd: "npm run dev -w api", Dir: "apps/api", Port: 4000},
		{ID: "app", Kind: knowledge.KindHost, Modes: h, Name: "app", Cmd: "npm run dev -w web", Dir: "apps/web", Port: 5173},
	}}
}

// graphEnv saves k as the checkout's knowledge, as a Start finds it.
func graphEnv(t *testing.T, plan knowledge.Plan, status string) GraphOpts {
	t.Helper()
	root := t.TempDir()
	k := &knowledge.Knowledge{Status: status, Plan: plan}
	if status == knowledge.StatusVerified {
		k.Verification = &knowledge.Verification{Mode: knowledge.ModeContainer, RecipeHash: plan.ExecutionHash(), RunID: 1}
	}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return GraphOpts{Root: root, Main: root, K: k, StateBase: t.TempDir(), HasSecrets: true,
		EntryTarget: "nginx:80/", EntryURL: "http://x.localhost"}
}

func TestPlanGraphDoesNotVerifyAnotherMode(t *testing.T) {
	plan := knowledge.Plan{Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "host", Kind: knowledge.KindHost, Name: "app", Cmd: "serve", Modes: []string{knowledge.ModeHybrid}},
	}}
	opts := graphEnv(t, plan, knowledge.StatusVerified)
	if node := gnode(t, PlanGraph(plan, knowledge.ModeContainer, opts), "up"); node.PlanState != knowledge.StatusVerified {
		t.Fatalf("verified mode lost its evidence: %+v", node)
	}
	if node := gnode(t, PlanGraph(plan, knowledge.ModeHybrid, opts), "up"); node.PlanState == knowledge.StatusVerified {
		t.Fatalf("another mode inherited verification: %+v", node)
	}
}

func gnode(t *testing.T, g Graph, id string) GraphNode {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no node %q in %v", id, g.Nodes)
	return GraphNode{}
}

func hasEdge(g Graph, from, to, label string) bool {
	for _, e := range g.Edges {
		if e == (GraphEdge{from, to, label}) {
			return true
		}
	}
	return false
}

func TestPlanGraphContainerVsHybrid(t *testing.T) {
	plan := hybridPlan()
	o := graphEnv(t, plan, knowledge.StatusVerified)
	if _, err := TrustHere(o.StateBase, o.Main, plan); err != nil {
		t.Fatal(err)
	}
	c := PlanGraph(plan, knowledge.ModeContainer, o)
	h := PlanGraph(plan, knowledge.ModeHybrid, o)

	if !reflect.DeepEqual(c.Parallel, [][]string{{"api", "app"}}) || !reflect.DeepEqual(h.Parallel, c.Parallel) {
		t.Fatalf("parallel: %v %v", c.Parallel, h.Parallel)
	}
	for _, e := range [][3]string{
		{"start", "secrets", ""}, {"secrets", "prepare", ""}, {"prepare", "mode", ""},
		{"mode", "up", "container"}, {"mode", "up-deps", "hybrid"},
		// an exec step between compose-up and the data step: the seed runs at its recorded place, after it
		{"up", "migrate", ""}, {"migrate", "new-volume", ""}, {"new-volume", "seed", "yes"}, {"new-volume", "ready", "no"}, {"seed", "ready", ""},
		{"up-deps", "migrate-host", ""}, {"migrate-host", "new-volume-hybrid", ""}, {"new-volume-hybrid", "seed-host", "yes"},
		{"seed-host", "api", "start together"}, {"seed-host", "app", "start together"}, {"new-volume-hybrid", "api", "no"},
		{"api", "ready-hybrid", ""}, {"app", "ready-hybrid", ""},
	} {
		if !hasEdge(c, e[0], e[1], e[2]) {
			t.Errorf("missing edge %v in %v", e, c.Edges)
		}
	}
	if d := gnode(t, c, "mode"); d.Kind != "decision" || !reflect.DeepEqual(d.Branches, []string{"container", "hybrid"}) {
		t.Errorf("mode: %+v", d)
	}
	if d := gnode(t, c, "new-volume"); d.Label != "New data volume?" || !reflect.DeepEqual(d.Branches, []string{"yes", "no"}) {
		t.Errorf("new-volume: %+v", d)
	}
	if r := gnode(t, c, "ready"); r.Kind != "terminal" || r.EntryURL != "http://x.localhost" || r.Label != "nginx:80/" {
		t.Errorf("ready: %+v", r)
	}

	// in_mode follows the viewed mode; shared nodes are always in.
	for id, want := range map[string][2]bool{ // container view, hybrid view
		"secrets": {true, true}, "prepare": {true, true}, "mode": {true, true}, "ready": {true, false}, "ready-hybrid": {false, true},
		"up": {true, false}, "migrate": {true, false}, "seed": {true, false}, "new-volume": {true, false},
		"up-deps": {false, true}, "migrate-host": {false, true}, "seed-host": {false, true}, "api": {false, true}, "app": {false, true},
		"new-volume-hybrid": {false, true},
	} {
		if got := gnode(t, c, id).InMode; got != want[0] {
			t.Errorf("container view: %s in_mode = %v", id, got)
		}
		if got := gnode(t, h, id).InMode; got != want[1] {
			t.Errorf("hybrid view: %s in_mode = %v", id, got)
		}
	}

	// where / state / command
	for id, w := range map[string][3]string{ // where, service, plan_state
		"secrets": {"stackfork", "", "at-start"}, "prepare": {"stackfork", "", "trusted"}, "up": {"docker", "", "verified"},
		"migrate": {"docker", "app", "verified"}, "migrate-host": {"host", "", "trusted"}, "api": {"host", "", "trusted"},
	} {
		n := gnode(t, c, id)
		if n.Where != w[0] || n.Service != w[1] || n.PlanState != w[2] || n.Kind != "step" {
			t.Errorf("%s: %+v, want %v", id, n, w)
		}
	}
	if n := gnode(t, c, "api"); n.Command != "npm run dev -w api  port 4000" || n.StepKind != "host" || n.CommandExpanded != "" {
		t.Errorf("api: %+v", n)
	}
	if n := gnode(t, c, "migrate"); n.Once != "schema" || n.Subtitle != "exec" || n.SkipReason != "" {
		t.Errorf("migrate: %+v", n)
	}
	if n := gnode(t, c, "up-deps"); n.Command != "compose up  without app, nginx" {
		t.Errorf("up-deps: %+v", n)
	}
}

func TestPlanGraphDraftSkipsSeedAndMigrate(t *testing.T) {
	plan := hybridPlan()
	plan.Status = knowledge.PlanProposed
	o := graphEnv(t, plan, knowledge.StatusDraft)
	g := PlanGraph(plan, knowledge.ModeHybrid, o)
	for _, id := range []string{"seed-host", "migrate-host"} {
		n := gnode(t, g, id)
		if n.PlanState != "draft" || !strings.Contains(n.SkipReason, "setup is a draft") {
			t.Errorf("%s: %+v", id, n)
		}
	}
	if n := gnode(t, g, "api"); n.PlanState != "untrusted" || n.SkipReason != "" {
		t.Errorf("host app of an untrusted draft: %+v", n)
	}
}

func TestPlanGraphSingleModeHasNoModeDecision(t *testing.T) {
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "app", Cmd: "m"},
	}}
	o := graphEnv(t, plan, knowledge.StatusVerified)
	o.HasSecrets = false
	for _, mode := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} { // hybrid is unsupported: same graph
		g := PlanGraph(plan, mode, o)
		for _, n := range g.Nodes {
			if n.ID == "mode" || n.ID == "new-volume" || !n.InMode {
				t.Errorf("%s: unexpected node %+v", mode, n)
			}
		}
		if !hasEdge(g, "prepare", "up", "") || !hasEdge(g, "migrate", "ready", "") { // the runner adds prepare
			t.Errorf("%s: edges %v", mode, g.Edges)
		}
	}
	if n := gnode(t, PlanGraph(plan, "container", o), "secrets"); n.SkipReason != "no secrets" {
		t.Errorf("secrets: %+v", n)
	}
}

func TestPlanGraphDerivedWithoutKnowledge(t *testing.T) {
	d := DerivedPlan{Mode: "container", Steps: []PlanStep{
		NewPlanStep(knowledge.Step{ID: "prepare", Kind: knowledge.KindPrepare}, StateDerived),
		NewPlanStep(knowledge.Step{ID: "up", Kind: knowledge.KindComposeUp}, StateDerived),
	}}
	plan := knowledge.Plan{}
	for _, s := range d.Steps {
		plan.Steps = append(plan.Steps, s.Step)
	}
	g := PlanGraph(plan, "container", GraphOpts{Root: t.TempDir(), Main: t.TempDir(), Derived: true, HasSecrets: true})
	var ids []string
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
		if n.Kind == "step" && n.ID != "secrets" && n.PlanState != "derived" {
			t.Errorf("%s: %s", n.ID, n.PlanState)
		}
	}
	if !reflect.DeepEqual(ids, []string{"start", "secrets", "prepare", "up", "ready"}) {
		t.Errorf("nodes %v", ids)
	}
	// an empty plan still draws the implicit prepare and up the runner adds
	g = PlanGraph(knowledge.Plan{}, "container", GraphOpts{Root: t.TempDir(), Main: t.TempDir()})
	if !hasEdge(g, "prepare", "up", "") || gnode(t, g, "up").PlanState != "derived" {
		t.Errorf("empty plan: %+v", g)
	}
}

// A seed that RunOrder places after other steps gets its decision there.
func TestPlanGraphDecisionFollowsTheSeedPosition(t *testing.T) {
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "build", Kind: knowledge.KindExec, Service: "app", Cmd: "b"},
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "app", Cmd: "s"},
	}}
	o := graphEnv(t, plan, knowledge.StatusVerified)
	order := PlanRunner{Root: o.Root, Main: o.Main, Plan: plan, Mode: knowledge.ModeContainer}.Steps()
	g := PlanGraph(plan, knowledge.ModeContainer, o)
	prev := "up"
	for _, s := range order[2:] { // after prepare, up
		if s.ID == "seed" {
			if !hasEdge(g, prev, "new-volume", "") || !hasEdge(g, "new-volume", "seed", "yes") {
				t.Errorf("edges %v", g.Edges)
			}
			return
		}
		prev = s.ID
	}
	t.Fatalf("no seed in %v", order)
}

// A decision followed by host apps that start together keeps its own branch
// labels; the group hangs off one branch and the decision never links to
// every member.
func TestPlanGraphDecisionBeforeParallelGroup(t *testing.T) {
	plan := hybridPlan()
	g := PlanGraph(plan, knowledge.ModeHybrid, graphEnv(t, plan, knowledge.StatusVerified))
	labels := []string{}
	for _, e := range g.Edges {
		if e.From == "new-volume-hybrid" {
			labels = append(labels, e.Label+"->"+e.To)
		}
	}
	if want := []string{"yes->seed-host", "no->api"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("seed decision edges = %v, want %v", labels, want)
	}
	if !hasEdge(g, "seed-host", "api", "start together") || !hasEdge(g, "seed-host", "app", "start together") {
		t.Errorf("an unlabelled frontier still links every member: %v", g.Edges)
	}
}

// The Mode decision keeps container and hybrid when the modes split on a group.
func TestPlanGraphModeSplitsOnParallelGroup(t *testing.T) {
	h := []string{knowledge.ModeHybrid}
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, AppService: "app", Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "api", Kind: knowledge.KindHost, Modes: h, Name: "api", Cmd: "npm run dev -w api", Port: 4000},
		{ID: "web", Kind: knowledge.KindHost, Modes: h, Name: "web", Cmd: "npm run dev -w web", Port: 5173},
	}}
	g := PlanGraph(plan, knowledge.ModeHybrid, graphEnv(t, plan, knowledge.StatusVerified))
	var out []string
	for _, e := range g.Edges {
		if e.From == "mode" {
			out = append(out, e.Label+"->"+e.To)
		}
	}
	if want := []string{"container->ready", "hybrid->api"}; !reflect.DeepEqual(out, want) {
		t.Errorf("mode edges = %v, want %v", out, want)
	}
}

// A step the modes share by id but not by command gets one node per mode
// (the seed entry can differ per mode).
func TestPlanGraphPlaceSplitsDifferentCommands(t *testing.T) {
	b := &graphBuilder{seen: map[string]int{}, inMode: map[string]map[string]bool{}, inPlan: map[string]bool{"seed": true}}
	seed := func(cmd string) knowledge.Step {
		return knowledge.Step{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "app", Cmd: cmd}
	}
	if a, c := b.place(seed("npm run seed:docker"), "container"), b.place(seed("npm run seed:docker"), "hybrid"); a != "seed" || c != "seed" {
		t.Errorf("same command shares a node: %s %s", a, c)
	}
	if id := b.place(seed("npm run seed:host"), "hybrid"); id != "seed-hybrid" {
		t.Errorf("another command gets its own node: %s", id)
	}
}
