package lifecycle

import (
	"bytes"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"io"
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

func proofStep(t *testing.T, dir, id string) *runs.Step {
	t.Helper()
	rec, err := runs.Begin(dir, runs.Meta{Command: "seed", Mode: knowledge.ModeContainer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Finish(nil) })
	return rec.Step(id, knowledge.KindExec, "seed")
}

func TestRunSeedGivesNoProofToACommandTheCapturedPlanDoesNotHold(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"app":{"build":"."}}}`), 0o600)
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "seed-original", Service: "app"}}}
	step := proofStep(t, dir, "seed")
	var out, errb bytes.Buffer
	RunSeed(&out, &errb, dir, dir, "br", "proj", dir, t.TempDir(), t.TempDir(), "seed-other", "app", knowledge.ModeContainer, plan, step, nil)
	if step.DefinitionHash != "" {
		t.Fatal("proof assigned to a command the captured plan does not record")
	}
}

func TestHostSeedRetryRecordsExecutionEvidence(t *testing.T) {
	dir, base := t.TempDir(), t.TempDir()
	seed := knowledge.Step{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnHost, Once: knowledge.OnceData, Cmd: "host-seed"}
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{seed}}
	if err := knowledge.WriteTrust(knowledge.TrustPath(base, dir), knowledge.PlanHash(plan.Steps), plan.Steps); err != nil {
		t.Fatal(err)
	}
	old := HostExec
	HostExec = func(io.Writer, string, string, string, string, string, knowledge.Step) (int, error) { return 0, nil }
	t.Cleanup(func() { HostExec = old })
	step := proofStep(t, dir, "seed")
	var out, errb bytes.Buffer
	if err := RunSeed(&out, &errb, dir, dir, "br", "proj", dir, base, t.TempDir(), "host-seed", "", knowledge.ModeContainer, plan, step, nil); err != nil {
		t.Fatal(err)
	}
	if want := TaskDefinitionHash(dir, seed, plan); step.ExecutedHash != want {
		t.Fatalf("executed hash %q, want %q", step.ExecutedHash, want)
	}
}

func TestPrepareRefreshesTheResolvedServiceInTaskHashes(t *testing.T) {
	dir := t.TempDir()
	install := knowledge.Step{ID: "install", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Cmd: "install", Once: knowledge.OnceDeps}
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{install}}
	rec, err := runs.Begin(dir, runs.Meta{Command: "start-env", Mode: knowledge.ModeContainer})
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Finish(nil)
	r := PlanRunner{Run: rec, InstanceDir: dir, Plan: plan, Mode: knowledge.ModeContainer}
	r.declare(plan.Steps)
	before := rec.Step("install", knowledge.KindExec, "").DefinitionHash
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"app":{"build":"."}}}`), 0o600) // what prepare writes
	r.refreshDefinitions(plan.Steps)
	if got := rec.Step("install", knowledge.KindExec, "").DefinitionHash; got == before || got != TaskDefinitionHash(dir, install, plan) {
		t.Fatalf("hash not refreshed after prepare: %q", got)
	}
}
