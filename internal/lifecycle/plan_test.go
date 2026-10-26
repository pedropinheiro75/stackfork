package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// The scan's own hybrid proposal must start in both modes: its exec steps may
// not target the app service that hybrid leaves out.
func TestProposedHybridPlanPassesCheckInBothModes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"dev":"nuxt dev","db:migrate":"x","db:seed":"y"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &knowledge.Knowledge{}
	k.Facts.Services = []knowledge.Service{
		{Name: "app", Build: true, Ports: []string{"3000:3000"}},
		{Name: "db", Ports: []string{"5432:5432"}},
	}
	k.Facts.Scripts = []knowledge.Script{{Name: "dev", Command: "nuxt dev", Source: "package.json"}}
	p := learn.ProposePlan(root, k)
	base := t.TempDir()
	if _, err := TrustHere(base, root, p); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
		if err := (PlanRunner{NewBase: base, Main: root, Mode: mode, Plan: p}).Check(); err != nil {
			t.Errorf("%s: %v", mode, err)
		}
	}
}

func TestCheckRefusesDataStepOnExcludedService(t *testing.T) {
	p := knowledge.Plan{AppService: "app", Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}},
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "s"},
	}}
	err := PlanRunner{NewBase: t.TempDir(), Main: t.TempDir(), Mode: knowledge.ModeContainer, Plan: p}.Check()
	if err == nil || !strings.Contains(err.Error(), "step seed runs in app") {
		t.Fatalf("%v", err)
	}
}

func implicitPrepareRun(t *testing.T, prepare func(io.Writer, []string) error, prepared func() bool) (r *runs.Run, dir string, err error) {
	t.Helper()
	dir = t.TempDir()
	rec, err := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli", Mode: knowledge.ModeContainer, Project: "p", Branch: "b",
		Secrets: func() []redact.Secret { return []redact.Secret{{Name: "TOKEN", Value: "hunter2-secret"}} }})
	if err != nil {
		t.Fatal(err)
	}
	runErr := PlanRunner{Out: io.Discard, Err: io.Discard, NewBase: t.TempDir(), Main: t.TempDir(), Root: t.TempDir(), InstanceDir: dir,
		Mode: knowledge.ModeContainer, Plan: knowledge.Plan{Steps: []knowledge.Step{{ID: "go", Kind: knowledge.KindComposeUp}}},
		Run: rec, Prepared: prepared, Prepare: prepare,
		Up: func(io.Writer, []string) error { return nil }}.Execute()
	rec.Finish(runErr)
	r, err = runs.Get(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir, runErr
}

// A plan without a prepare step still prepares before its first compose-up,
// and that is a step of the run: declared, logged, redacted, able to fail.
func TestImplicitPrepareIsAStepThatCanFail(t *testing.T) {
	r, dir, err := implicitPrepareRun(t, func(w io.Writer, _ []string) error {
		fmt.Fprintln(w, "using token hunter2-secret")
		return errors.New("boom")
	}, func() bool { return false })
	if err == nil || r.FailedStep() != "prepare" {
		t.Fatalf("%v failed step %q", err, r.FailedStep())
	}
	if len(r.Steps) != 2 || r.Steps[0].ID != "prepare" || r.Steps[1].ID != "go" || r.Steps[1].State != runs.Pending {
		t.Fatalf("steps %+v", r.Steps)
	}
	log, lerr := runs.Log(dir, 1, "prepare")
	if lerr != nil || !strings.Contains(string(log), "using token") || strings.Contains(string(log), "hunter2-secret") {
		t.Fatalf("log %q %v", log, lerr)
	}
}

func TestImplicitPrepareIsSkippedWhenNotNeeded(t *testing.T) {
	r, _, err := implicitPrepareRun(t, func(io.Writer, []string) error { t.Fatal("prepared again"); return nil }, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Steps[0]; s.ID != "prepare" || s.State != runs.Skipped || s.Reason != "already prepared" {
		t.Fatalf("%+v", s)
	}
}

func TestHostExecNeedsTrust(t *testing.T) {
	p := knowledge.Plan{Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Cmd: "bun run db:migrate", Once: knowledge.OnceSchema},
	}}
	if !NeedsTrust(p, knowledge.ModeContainer) {
		t.Fatal("a host exec step needs trust in container mode")
	}
	base, root := t.TempDir(), t.TempDir()
	if ok, diff := HashTrusted(base, root, p); ok || len(diff) == 0 {
		t.Fatalf("untrusted: %v %v", ok, diff)
	}
	if _, err := TrustHere(base, root, p); err != nil {
		t.Fatal(err)
	}
	if ok, _ := HashTrusted(base, root, p); !ok {
		t.Fatal("trusted after approval")
	}
	if b := StepBlock(p.Steps[1]); !strings.Contains(strings.Join(b, "\n"), "on: host") {
		t.Fatalf("StepBlock %v", b)
	}
	if NeedsTrust(knowledge.Plan{Steps: p.Steps[:1]}, knowledge.ModeContainer) {
		t.Fatal("no host-run step, no trust")
	}
}

func TestHostSeedEntryNamesBothCommands(t *testing.T) {
	root := t.TempDir()
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Once: knowledge.OnceSchema, Cmd: "bun run db:migrate", Dir: "apps/api"},
		{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnHost, Once: knowledge.OnceData, Cmd: "bun run db:seed", Dir: "apps/api"},
	}}
	if err := (&knowledge.Knowledge{Schema: knowledge.CurrentSchema, Plan: plan}).Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	e, ok := SeedEntry(root, root, "", plan, knowledge.ModeContainer)
	if !ok || StepText(e) != "bun run db:migrate; then bun run db:seed  on host" {
		t.Fatalf("%v %q", ok, StepText(e))
	}
	plan.Steps[1].Dir = "apps/web"
	if err := (&knowledge.Knowledge{Schema: knowledge.CurrentSchema, Plan: plan}).Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	if e, _ := SeedEntry(root, root, "", plan, knowledge.ModeContainer); StepText(e) != "bun run db:seed  on host" {
		t.Fatalf("%q", StepText(e))
	}
}

// Steps is the list a run declares: the implicit prepare before the first
// compose-up, an implicit up when the plan has none, and the plan's data step
// moved right after the first compose-up. Exact keeps the plan as it is.
func TestStepsIsTheDeclaredList(t *testing.T) {
	ids := func(l []knowledge.Step) string {
		var s []string
		for _, x := range l {
			s = append(s, x.ID)
		}
		return strings.Join(s, ",")
	}
	r := PlanRunner{NewBase: t.TempDir(), Main: t.TempDir(), Root: t.TempDir(), InstanceDir: t.TempDir(), Mode: knowledge.ModeContainer}
	if got := ids(r.Steps()); got != "prepare,up" {
		t.Errorf("empty plan: %s", got)
	}
	r.Plan = knowledge.Plan{Steps: []knowledge.Step{
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "s", Service: "app"},
		{ID: "go", Kind: knowledge.KindComposeUp},
		{ID: "after", Kind: knowledge.KindNote, Text: "n"},
	}}
	if got := ids(r.Steps()); got != "prepare,go,seed,after" {
		t.Errorf("plan: %s", got)
	}
	r.Exact = true
	if got := ids(r.Steps()); got != "go,seed,after" {
		t.Errorf("exact: %s", got)
	}
}

// In hybrid mode a host step replaces the Compose service of its name: the
// compose-up leaves it out without an exclude; container mode is unchanged.
func TestHybridHostStepExcludesTheServiceOfItsName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"app":{},"db":{},"ui":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := knowledge.Plan{Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "app", Kind: knowledge.KindHost, Name: "app", Cmd: "x", Modes: []string{knowledge.ModeHybrid}},
		{ID: "web", Kind: knowledge.KindHost, Name: "web", Cmd: "x", Modes: []string{knowledge.ModeHybrid}}, // no such service
	}}
	names, all, err := StartedServices(dir, p, knowledge.ModeHybrid)
	if err != nil || all || strings.Join(names, ",") != "db,ui" {
		t.Fatalf("hybrid: %v all=%v err=%v", names, all, err)
	}
	if _, all, _ := StartedServices(dir, p, knowledge.ModeContainer); !all {
		t.Fatal("container mode must start every service")
	}
	steps := PlanRunner{InstanceDir: dir, Plan: p, Mode: knowledge.ModeHybrid, Exact: true}.Steps()
	if len(steps) == 0 || steps[0].Kind != knowledge.KindComposeUp || strings.Join(steps[0].Exclude, ",") != "app" {
		t.Fatalf("plan steps show no exclude: %+v", steps)
	}
	if p.Steps[0].Exclude != nil {
		t.Fatal("the plan itself was changed")
	}
}
