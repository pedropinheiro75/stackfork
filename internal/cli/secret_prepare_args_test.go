package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const exploitRef = "op://Private/bank/password"

var exploitStep = knowledge.Step{ID: "prepare", Kind: knowledge.KindPrepare,
	Args: []string{"-f", "x.yaml", "--secret", "NPM_TOKEN=" + exploitRef}}

// Review C1: a plan cannot inject --secret into prepare. Refused when the
// plan loads (start-env fails before Docker) and, past validation, by
// prepareArgs; nothing is prepared and the fake op is never called.
func TestStartEnvRefusesKnowledgePrepareSecretFlag(t *testing.T) {
	root, state := reposEnv(t)
	repo := adaptedRepo(t, root)
	body := `{"schema":2,"status":"verified","facts":{},"plan":{"status":"recorded","steps":[` +
		`{"id":"prepare","kind":"prepare","args":["-f","x.yaml","--secret","NPM_TOKEN=` + exploitRef + `"]},{"id":"up","kind":"compose-up"}]}}`
	os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755)
	if err := os.WriteFile(filepath.Join(repo, ".stackfork", "knowledge.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t)
	calls := fakeOp(t, "stolen-value-1234")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "--secret") {
		t.Fatalf("exploit plan started: %d\n%s\n%s", code, out, e)
	}
	if strings.Contains(out+e, "bank") || strings.Contains(out+e, "stolen-value") {
		t.Errorf("output echoes the reference or value:\n%s\n%s", out, e)
	}
	if ms, _ := filepath.Glob(filepath.Join(state, "*", "manifest.json")); len(ms) != 0 {
		t.Errorf("a manifest was written: %v", ms)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("op was called")
	}
}

func TestPlanPrepareArgsRefusesSecretFlagPastValidation(t *testing.T) {
	k := &knowledge.Knowledge{Plan: knowledge.Plan{Steps: []knowledge.Step{exploitStep}}}
	args, err := planPrepareArgs(k)
	if err == nil || !strings.Contains(err.Error(), "--secret") || strings.Contains(err.Error(), "bank") || args != nil {
		t.Fatalf("args %v err %v", args, err)
	}
}
