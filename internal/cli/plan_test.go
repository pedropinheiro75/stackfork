package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
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
		Mode        string `json:"mode"`
		Status      string `json:"status"`
		Trusted     bool   `json:"trusted"`
		TrustNeeded bool   `json:"trust_needed"`
		Steps       []struct{ ID, Trust, State string }
		Skipped     []struct{ ID, Trust, State string }
	}
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatal(err, out)
	}
	if j.Mode != "container" || j.Status != "recorded" || !j.Trusted || j.TrustNeeded || len(j.Steps) != 2 || len(j.Skipped) != 1 ||
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
	if ok, _ := lifecycle.TrustedHere(state, root, k.Plan, "hybrid"); !ok {
		t.Fatal("must be trusted after plan --trust")
	}
	k.Plan.Steps[2].Cmd = "npm run evil"
	ok, diff := lifecycle.TrustedHere(state, root, k.Plan, "hybrid")
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

func TestPlanTrustShowsAllHashedFieldsAndRejectsBadMode(t *testing.T) {
	root := planFixture(t)
	k, _ := knowledge.Load(knowledgePath(root))
	k.Plan.Steps[2].Text = "starts the web app"
	k.Plan.Steps[2].Service = "web-svc"
	k.Plan.Steps[2].Dir = "web"
	k.Plan.Steps[2].PortEnv = []string{"PORT", "NUXT_PORT"}
	if err := k.Save(knowledgePath(root)); err != nil {
		t.Fatal(err)
	}
	out, _, code := runCLI(t, "--root", root, "--state-dir", t.TempDir(), "plan", "--trust")
	if code != 0 || !strings.Contains(out, "dir: web") || !strings.Contains(out, "NUXT_PORT") ||
		!strings.Contains(out, "starts the web app") || !strings.Contains(out, "web-svc") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	out, _, _ = runCLI(t, "--root", root, "plan", "--mode", "hybrid")
	if !strings.Contains(out, "dir: web") {
		t.Fatalf("plan text must show dir:\n%s", out)
	}
	_, stderr, code := runCLI(t, "--root", root, "plan", "--mode", "bogus")
	if code != 1 || !strings.Contains(stderr, "container") || !strings.Contains(stderr, "hybrid") {
		t.Fatalf("code=%d %s", code, stderr)
	}
}

// A Step with every field set must print one line per field except id and
// kind (the header), so a future Step field shows up without touching plan.go.
func TestStepBlockPrintsEveryStepField(t *testing.T) {
	var s knowledge.Step
	v := reflect.ValueOf(&s).Elem()
	for i := 0; i < v.NumField(); i++ {
		switch f := v.Field(i); f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Int:
			f.SetInt(7)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{"a", "b"}))
		}
	}
	if got, want := len(lifecycle.StepBlock(s)), 1+v.NumField()-2; got != want {
		t.Fatalf("stepBlock printed %d lines, want %d:\n%s", got, want, strings.Join(lifecycle.StepBlock(s), "\n"))
	}
}

// A host exec step prints with all its fields (StepBlock) and its trust
// state in text and JSON, before and after this machine trusts the plan.
func TestPlanShowsHostExecStepsWithTrust(t *testing.T) {
	root := initRepo(t)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusDraft}
	k.Plan = knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Cmd: "bun run db:migrate", Once: knowledge.OnceSchema, Dir: "apps/api"},
		{ID: "cmigrate", Kind: knowledge.KindExec, Service: "web", Cmd: "m"},
	}}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	for _, phase := range []string{"untrusted", "trusted"} {
		if phase == "trusted" {
			if _, e, code := runCLI(t, "--root", root, "--state-dir", state, "plan", "--trust"); code != 0 {
				t.Fatal(e)
			}
		}
		txt, e, code := runCLI(t, "--root", root, "--state-dir", state, "plan", "--mode", "container")
		if code != 0 {
			t.Fatal(e)
		}
		assertGolden(t, "plan-host-exec-"+phase+".txt", txt)
		js, _, _ := runCLI(t, "--root", root, "--state-dir", state, "plan", "--mode", "container", "--json")
		assertGolden(t, "plan-host-exec-"+phase+".json", js)
	}
}
