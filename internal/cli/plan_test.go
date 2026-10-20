package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

func planFixture(t *testing.T) (root string) {
	t.Helper()
	root = initRepo(t)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusDraft}
	k.Plan = knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", "compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}},
		{ID: "app", Kind: knowledge.KindHost, Cmd: "npm run dev", Name: "app", Modes: []string{"hybrid"}},
	}}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPlanTextShowsStatesAndSkipped(t *testing.T) {
	root := planFixture(t)
	out, stderr, code := runCLI(t, "--root", root, "plan", "--mode", "container")
	if code != 0 {
		t.Fatalf("code=%d %s", code, stderr)
	}
	i := strings.Index(out, "skipped in this mode")
	if i < 0 || !strings.Contains(out[i:], "app") || !strings.Contains(out[i:], "untrusted") || !strings.Contains(out, "port: unknown") {
		t.Fatalf("output:\n%s", out)
	}
	out, _, _ = runCLI(t, "--root", root, "plan", "--mode", "container", "--json")
	var j struct {
		Mode    string `json:"mode"`
		Status  string `json:"status"`
		Trusted bool   `json:"trusted"`
		Steps   []struct{ ID, Trust, State string }
		Skipped []struct{ ID, Trust, State string }
	}
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatal(err, out)
	}
	if j.Mode != "container" || j.Status != "recorded" || j.Trusted || len(j.Steps) != 2 || len(j.Skipped) != 1 ||
		j.Skipped[0].Trust != "untrusted" || j.Skipped[0].State != "recorded" {
		t.Fatalf("%+v", j)
	}
}

func TestPlanTrustWritesFileAndStartIsTrusted(t *testing.T) {
	root := planFixture(t)
	state := t.TempDir()
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", state, "plan", "--trust")
	if code != 0 || !strings.Contains(out, "npm run dev") {
		t.Fatalf("code=%d %s %s", code, out, stderr)
	}
	p := knowledgePath(root)
	k, _ := knowledge.Load(p)
	if ok, _ := lifecycle.TrustedHere(state, root, k.Plan); !ok {
		t.Fatal("must be trusted after plan --trust")
	}
	k.Plan.Steps[2].Cmd = "npm run evil"
	ok, diff := lifecycle.TrustedHere(state, root, k.Plan)
	if ok || !strings.Contains(strings.Join(diff, "\n"), "~ app:") {
		t.Fatalf("ok=%v diff=%v", ok, diff)
	}
}

func TestPlanTrustRefusedWithoutHostStep(t *testing.T) {
	root := initRepo(t)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusDraft}
	k.Plan = knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}}}
	if err := k.Save(knowledgePath(root)); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", t.TempDir(), "plan", "--trust")
	if code != 2 || !strings.Contains(stderr, "nothing to trust") {
		t.Fatalf("code=%d %s", code, stderr)
	}
}
