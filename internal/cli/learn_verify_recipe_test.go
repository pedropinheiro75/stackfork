package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// recipeVerify is a checkout prepared with the given args and a recorded
// plan (steps) whose entry is `none`, so verify needs no running stack.
type recipeVerify struct {
	root  string
	flags []string
	path  string
}

func newRecipeVerify(t *testing.T, prepare []string, steps []knowledge.Step) recipeVerify {
	t.Helper()
	installFakeDocker(t)
	root := initRepo(t)
	os.MkdirAll(filepath.Join(root, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(gapBase), 0o644)
	os.WriteFile(filepath.Join(root, ".stackfork", "dev.compose.yaml"), []byte(gapOverride), 0o644)
	os.WriteFile(filepath.Join(root, ".stackfork", "runtime.env"), []byte("A=1\n"), 0o600)
	flags := []string{"--state-dir", filepath.Join(t.TempDir(), "state"), "--legacy-state-dir", t.TempDir()}
	if prepare != nil {
		if out, e, code := runCLI(t, append(append(append([]string{}, flags...), "--root", root, "prepare"), prepare...)...); code != 0 {
			t.Fatalf("prepare: %s %s", out, e)
		}
	}
	k := &knowledge.Knowledge{Status: knowledge.StatusDraft, Entry: &knowledge.Entry{None: true},
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: steps}}
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	return recipeVerify{root: root, flags: flags, path: path}
}

func (v recipeVerify) verify(t *testing.T) (string, string, int) {
	t.Helper()
	return runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "learn", "verify", "--check", "app loads", "--confirm", "yes")...)
}

func (v recipeVerify) prepareStep(t *testing.T) (idx int, args string) {
	t.Helper()
	k, err := knowledge.Load(v.path)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range k.Plan.Steps {
		if s.Kind == knowledge.KindPrepare {
			return i, strings.Join(s.Args, " ")
		}
	}
	return -1, ""
}

func (v recipeVerify) verifyExecuted(t *testing.T) (string, string, int) {
	t.Helper()
	recordVerifyRun(t, v.root, v.flags)
	return v.verify(t)
}

// prepareForVerify prepares the checkout's compose.yaml, so `learn verify`
// finds the instance whose recipe it records.
func prepareForVerify(t *testing.T, root string) {
	t.Helper()
	installFakeDocker(t)
	if out, e, code := runCLI(t, "--root", root, "prepare", "-f", "compose.yaml"); code != 0 {
		t.Fatalf("prepare: %s %s", out, e)
	}
	recordVerifyRun(t, root, nil)
}

var fullPrepare = []string{"-f", "compose.yaml", "-f", ".stackfork/dev.compose.yaml", "--env-file", ".stackfork/runtime.env"}

func TestLearnVerifyCompletesAShortPrepareStep(t *testing.T) {
	v := newRecipeVerify(t, fullPrepare, []knowledge.Step{
		{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", ".stackfork/dev.compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	})
	out, e, code := v.verifyExecuted(t)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	want := "-f compose.yaml -f .stackfork/dev.compose.yaml --env-file .stackfork/runtime.env"
	if _, got := v.prepareStep(t); got != want {
		t.Fatalf("args %q want %q", got, want)
	}
	var j struct {
		Prepare []string `json:"prepare"`
	}
	if err := json.Unmarshal([]byte(out), &j); err != nil || strings.Join(j.Prepare, " ") != want {
		t.Fatalf("json prepare %v %v\n%s", j.Prepare, err, out)
	}
}

func TestLearnVerifyInsertsPrepareBeforeComposeUp(t *testing.T) {
	v := newRecipeVerify(t, fullPrepare, []knowledge.Step{
		{ID: "net", Kind: knowledge.KindNote, Text: "x"},
		{ID: "up", Kind: knowledge.KindComposeUp},
	})
	if out, e, code := v.verifyExecuted(t); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	k, _ := knowledge.Load(v.path)
	var ids []string
	for _, s := range k.Plan.Steps {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "net,prepare,up" || k.Plan.Steps[1].Kind != knowledge.KindPrepare {
		t.Fatalf("steps %v", ids)
	}
}

func TestLearnVerifyKeepsTheOtherPrepareFlags(t *testing.T) {
	v := newRecipeVerify(t, fullPrepare, []knowledge.Step{
		{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", "old.yaml", "--hostname", "acme.localhost", "--reassign-ports"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	})
	if out, e, code := v.verifyExecuted(t); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	want := "-f compose.yaml -f .stackfork/dev.compose.yaml --env-file .stackfork/runtime.env --hostname acme.localhost --reassign-ports"
	if _, got := v.prepareStep(t); got != want {
		t.Fatalf("args %q want %q", got, want)
	}
}

func TestLearnVerifyLeavesAStepWhoseFileIsOutsideTheCheckout(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.compose.yaml")
	os.WriteFile(outside, []byte(gapBase), 0o644)
	v := newRecipeVerify(t, []string{"-f", outside}, []knowledge.Step{
		{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", "compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	})
	out, e, code := v.verifyExecuted(t)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if _, got := v.prepareStep(t); got != "-f compose.yaml" {
		t.Fatalf("step changed: %q", got)
	}
	if !strings.Contains(out, "outside the checkout; the prepare step was not changed") {
		t.Fatalf("no warning:\n%s", out)
	}
}

func TestLearnVerifyRefusesWithoutAPreparedInstance(t *testing.T) {
	v := newRecipeVerify(t, nil, []knowledge.Step{
		{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", "compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	})
	before, _ := os.ReadFile(v.path)
	_, e, code := v.verify(t)
	if code == 0 || !strings.Contains(e, "no prepared instance for this checkout; prepare it, then verify") {
		t.Fatalf("%d %s", code, e)
	}
	if after, _ := os.ReadFile(v.path); string(after) != string(before) {
		t.Fatal("the knowledge file changed")
	}
}

func TestLearnVerifyTrustNoteWhenThePrepareStepChanged(t *testing.T) {
	v := newRecipeVerify(t, fullPrepare, []knowledge.Step{
		{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", ".stackfork/dev.compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "app", Kind: knowledge.KindHost, Cmd: "npm run dev", Name: "app", Modes: []string{"hybrid"}},
	})
	if _, e, code := runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "plan", "--trust")...); code != 0 {
		t.Fatal(e)
	}
	planJSON := func() (trusted bool) {
		out, _, _ := runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "plan", "--mode", "hybrid", "--json")...)
		var j struct {
			Trusted bool `json:"trusted"`
		}
		json.Unmarshal([]byte(out), &j)
		return j.Trusted
	}
	if !planJSON() {
		t.Fatal("the plan must be trusted before verify")
	}
	out, e, code := v.verifyExecuted(t)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	var j struct {
		Note string `json:"note"`
	}
	json.Unmarshal([]byte(out), &j)
	if !strings.Contains(j.Note, "the prepare step changed; a person must trust the plan again: stackfork plan --trust") {
		t.Fatalf("note %q\n%s", j.Note, out)
	}
	if planJSON() {
		t.Fatal("the plan must be untrusted after the prepare step changed")
	}
	// a plan without host steps needs no trust note
	w := newRecipeVerify(t, fullPrepare, []knowledge.Step{
		{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", ".stackfork/dev.compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	})
	if out, _, _ := w.verifyExecuted(t); strings.Contains(out, "trust") {
		t.Fatalf("unexpected trust note:\n%s", out)
	}
}
