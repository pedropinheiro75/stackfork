package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOneOffExecIsExplicit(t *testing.T) {
	plan := Plan{Steps: []Step{{ID: "install", Kind: KindExec, On: "one-off", Service: "app", Cmd: "bun install", Once: OnceDeps}}}
	if err := ValidatePlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.Steps[0].On = "typo"
	if err := ValidatePlan(plan); err == nil {
		t.Fatal("unknown execution target accepted")
	}
}

func TestLegacyVerificationNeedsExecutionEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.json")
	k := &Knowledge{Status: StatusVerified, Plan: Plan{Status: PlanRecorded, Steps: []Step{{ID: "up", Kind: KindComposeUp}}}, Verification: &Verification{Date: "2026-11-09"}}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status == StatusVerified {
		t.Fatal("legacy verification without a successful recipe run remains verified")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("loading changed the stored knowledge")
	}
}

func TestVerificationTracksCompletePlan(t *testing.T) {
	k := &Knowledge{Status: StatusVerified, Plan: Plan{Status: PlanRecorded, Steps: []Step{{ID: "up", Kind: KindComposeUp}}}}
	k.Verification = &Verification{RecipeHash: k.Plan.ExecutionHash(), Mode: ModeContainer, RunID: 4}
	if !k.VerificationCurrent() {
		t.Fatal("matching evidence rejected")
	}
	for _, change := range []func(*Plan){
		func(plan *Plan) { plan.Steps[0].Exclude = []string{"app"} },
		func(plan *Plan) { plan.NoSeed = true },
		func(plan *Plan) { plan.AppService = "other" },
		func(plan *Plan) { plan.Modes = []string{ModeHybrid} },
	} {
		plan := k.Plan
		plan.Steps = append([]Step(nil), k.Plan.Steps...)
		change(&plan)
		changed := *k
		changed.Plan = plan
		if changed.VerificationCurrent() {
			t.Fatal("changed recipe still verified")
		}
	}
}
