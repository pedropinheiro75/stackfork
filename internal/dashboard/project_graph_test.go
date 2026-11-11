package dashboard_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// hybridGraphProject is what /api/project returns for a hybrid plan with host
// apps that start together and a seed, built with the real PlanGraph.
func hybridGraphProject(t *testing.T) map[string]any {
	t.Helper()
	c, h := []string{knowledge.ModeContainer}, []string{knowledge.ModeHybrid}
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, AppService: "app", Steps: []knowledge.Step{
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
	root := t.TempDir()
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified, Plan: plan}
	if err := verifiedFixture(k).Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	o := lifecycle.GraphOpts{Root: root, Main: root, K: k, StateBase: t.TempDir(), HasSecrets: true, EntryTarget: "nginx:80/", EntryURL: "http://x.localhost"}
	return map[string]any{"name": "acme", "default_mode": "container", "modes": []string{"container", "hybrid"}, "checkouts": []any{},
		"workflow": map[string]any{"container": lifecycle.PlanGraph(plan, "container", o), "hybrid": lifecycle.PlanGraph(plan, "hybrid", o)}}
}

// The canvas draws PlanGraph output as designed: each step once, one Ready per
// mode branch, the seed decision per branch, host apps in one parallel group.
func TestWorkflowCanvasDrawsPlanGraph(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	b, _ := json.Marshal(hybridGraphProject(t))
	fixture := filepath.Join(t.TempDir(), "project.json")
	if err := os.WriteFile(fixture, b, 0o600); err != nil {
		t.Fatal(err)
	}
	script, _ := filepath.Abs("testdata/workflow_plangraph.js")
	out, err := exec.Command(node, script, fixture).CombinedOutput()
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(out)), "ok") {
		t.Fatalf("workflow_plangraph.js: %v\n%s", err, out)
	}
}
