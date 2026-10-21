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

// Round 2, C1 via adapter_files: the adapter files become -f values. Refused
// at Load; and prepareArgs checks what it returns from every source.
func TestKnowledgePrepareArgsChecksAdapterFiles(t *testing.T) {
	k := &knowledge.Knowledge{AdapterFiles: []string{"--npmrc=evil.compose.yaml", ".agent-dev/app.compose.yaml"}}
	args, err := knowledgePrepareArgs(k)
	if err == nil || args != nil || !strings.Contains(err.Error(), "adapter_files") {
		t.Fatalf("args %v err %v", args, err)
	}
	k = &knowledge.Knowledge{AdapterFiles: []string{".agent-dev/app.compose.yaml"}}
	if args, err := knowledgePrepareArgs(k); err != nil || strings.Join(args, " ") != "-f .agent-dev/app.compose.yaml" {
		t.Fatalf("args %v err %v", args, err)
	}
}

func TestStartEnvRefusesAdapterFilesThatAreFlags(t *testing.T) {
	root, state := reposEnv(t)
	repo := adaptedRepo(t, root)
	outside := filepath.Join(t.TempDir(), "npmrc")
	const canary = "npm-token-canary-4455"
	os.WriteFile(outside, []byte("//npm.pkg.github.com/:_authToken="+canary+"\n"), 0o600)
	os.Symlink(outside, filepath.Join(repo, "evil.compose.yaml"))
	os.WriteFile(filepath.Join(repo, "-f"), []byte("x"), 0o644)
	body := `{"schema":2,"status":"verified","facts":{},"plan":{},"adapter_files":["--npmrc=evil.compose.yaml",".agent-dev/app.compose.yaml"]}`
	os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(repo, ".stackfork", "knowledge.json"), []byte(body), 0o644)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "adapter_files") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if ms, _ := filepath.Glob(filepath.Join(state, "*", "manifest.json")); len(ms) != 0 {
		t.Errorf("manifest written: %v", ms)
	}
	runCLI(t, append(base, "--root", repo, "up", "--wait-timeout", "5")...)
	if strings.Contains(dockerEnvLog(log), canary) {
		t.Error("the token reached docker")
	}
}
