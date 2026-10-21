package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planDerivedRepo is a ready repo with an adapter, an auto-detected seed and
// no plan steps.
func planDerivedRepo(t *testing.T) (f deriveFix, args []string) {
	t.Helper()
	f = newDeriveFix(t)
	f.write(t, f.repo, derivedAdapter, "services: {}\n")
	f.seedable(t)
	f.verified(t, f.repo)
	return f, append([]string{"--root", f.repo}, f.base()...)
}

func TestPlanPrintsTheDerivedPlan(t *testing.T) {
	_, args := planDerivedRepo(t)
	txt, e, code := runCLI(t, append(args, "plan")...)
	if code != 0 || e != "" {
		t.Fatalf("%d %s", code, e)
	}
	assertGolden(t, "plan-derived.txt", txt)
	js, _, code := runCLI(t, append(args, "plan", "--json")...)
	if code != 0 {
		t.Fatal(js)
	}
	assertGolden(t, "plan-derived.json", js)
	var out struct {
		Derived, Trusted bool
		TrustNeeded      bool `json:"trust_needed"`
		Status           string
		PrepareFrom      string `json:"prepare_from"`
		Steps            []struct{ Source string }
	}
	if json.Unmarshal([]byte(js), &out) != nil || !out.Derived || !out.Trusted || out.TrustNeeded || out.Status != "derived" || out.PrepareFrom != "adapter" || len(out.Steps) != 3 || out.Steps[0].Source == "" {
		t.Errorf("%s", js)
	}
}

func TestPlanWithoutKnowledgeExitsZero(t *testing.T) {
	f := newDeriveFix(t)
	f.write(t, f.repo, derivedAdapter, "services: {}\n")
	args := append([]string{"--root", f.repo}, f.base()...)
	txt, e, code := runCLI(t, append(args, "plan")...)
	if code != 0 || !strings.HasPrefix(txt, "plan (derived — not recorded; run learn to record it, mode container)\n") ||
		!strings.Contains(txt, "start-env would refuse this checkout: ") {
		t.Fatalf("%d\n%s\n%s", code, txt, e)
	}
	assertGolden(t, "plan-derived-refusal.txt", txt)
	js, _, code := runCLI(t, append(args, "plan", "--json")...)
	var out struct {
		Derived bool
		Refusal string
		Steps   []any
	}
	if code != 0 || json.Unmarshal([]byte(js), &out) != nil || !out.Derived || out.Refusal == "" || out.Steps == nil || len(out.Steps) != 0 {
		t.Errorf("%d %s", code, js)
	}
}

func TestPlanTrustOnADerivedPlanHasNothingToTrust(t *testing.T) {
	_, args := planDerivedRepo(t)
	_, e, code := runCLI(t, append(args, "plan", "--trust")...)
	if code != 2 || !strings.Contains(e, "nothing to trust: this plan runs only in containers") {
		t.Errorf("%d %s", code, e)
	}
}

func TestPlanBrokenKnowledgeStillFails(t *testing.T) {
	f, args := planDerivedRepo(t)
	os.WriteFile(filepath.Join(f.repo, ".stackfork", "knowledge.json"), []byte("{not json"), 0o644)
	_, e, code := runCLI(t, append(args, "plan")...)
	if code != 1 || !strings.Contains(e, "no startup plan; run `stackfork learn` first") {
		t.Errorf("%d %s", code, e)
	}
}
