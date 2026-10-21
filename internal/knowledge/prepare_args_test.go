package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretRef = "op://Private/bank/password"

// A committed knowledge.json must not carry a --secret into prepare: the
// preflight never sees it and Execute would resolve it late (review C1).
func TestPrepareArgsAllowlist(t *testing.T) {
	ok := [][]string{
		nil,
		{"-f", "a.yaml", "--file", "b.yaml", "--env-file", ".env.dev", "--hostname", "app.localhost", "--reassign-ports", "--random-ports"},
	}
	for _, a := range ok {
		if err := ValidatePlan(Plan{Steps: []Step{{ID: "p", Kind: KindPrepare, Args: a}}}); err != nil {
			t.Errorf("%v refused: %v", a, err)
		}
	}
	bad := map[string][]string{
		"--secret":        {"-f", "x.yaml", "--secret", "NPM_TOKEN=" + secretRef},
		"--secret=":       {"--secret=NPM_TOKEN=" + secretRef},
		"--npmrc":         {"--npmrc", "/tmp/npmrc"},
		"--root":          {"--root", "/elsewhere"},
		"--state-dir":     {"--state-dir", "/elsewhere"},
		"--instance":      {"--instance", "other"},
		"a stray value":   {"NPM_TOKEN=" + secretRef},
		"missing value":   {"-f"},
		"flag as value":   {"-f", "--secret"},
		"metachar value":  {"-f", "a.yaml;rm"},
		"dollar value":    {"--env-file", "$HOME/x"},
		"unknown flag":    {"--volumes"},
		"flag with value": {"--random-ports=1"},
	}
	for name, a := range bad {
		err := ValidatePlan(Plan{Steps: []Step{{ID: "p", Kind: KindPrepare, Args: a}}})
		if err == nil {
			t.Errorf("%s: accepted %v", name, a)
			continue
		}
		if strings.Contains(err.Error(), "op://") || strings.Contains(err.Error(), "bank") {
			t.Errorf("%s: the error echoes the value: %v", name, err)
		}
	}
	err := ValidatePlan(Plan{Steps: []Step{{ID: "p", Kind: KindPrepare, Args: bad["--secret"]}}})
	if err == nil || !strings.Contains(err.Error(), "--secret") || !strings.Contains(err.Error(), `"p"`) {
		t.Errorf("the error must name the step and the flag: %v", err)
	}
}

func TestLoadRefusesPrepareStepWithSecretFlag(t *testing.T) {
	p := filepath.Join(t.TempDir(), "knowledge.json")
	body := `{"schema":2,"status":"verified","facts":{},"plan":{"status":"recorded","steps":[` +
		`{"id":"prepare","kind":"prepare","args":["-f","x.yaml","--secret","NPM_TOKEN=` + secretRef + `"]}]}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "--secret") || strings.Contains(err.Error(), "bank") {
		t.Fatalf("err %v", err)
	}
}

// Round 2, C1 via adapter_files: prepare gets the adapter files as -f
// values, and splitArgs lifts --npmrc=, --secret= and --state-dir= out of
// argv, so an entry that starts with '-' is a flag, not a file.
func TestLoadRefusesUnsafeAdapterFiles(t *testing.T) {
	for _, bad := range []string{"--npmrc=evil.compose.yaml", "-f", "--secret=X=env:Y.compose.yaml", "/etc/evil.compose.yaml",
		"../evil.compose.yaml", "a/../b.compose.yaml", "./a.compose.yaml", "", "a\nb.compose.yaml"} {
		body, _ := json.Marshal(map[string]any{"schema": 2, "status": "verified", "facts": map[string]any{}, "plan": map[string]any{}, "adapter_files": []string{".agent-dev/ok.compose.yaml", bad}})
		p := filepath.Join(t.TempDir(), "knowledge.json")
		os.WriteFile(p, body, 0o644)
		if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "adapter_files") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	body := `{"schema":2,"status":"verified","facts":{},"plan":{},"adapter_files":[".stackfork/compose.override.yaml",".agent-dev/app.compose.yaml","dir/sub"]}`
	p := filepath.Join(t.TempDir(), "knowledge.json")
	os.WriteFile(p, []byte(body), 0o644)
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	k := &Knowledge{AdapterFiles: []string{"--npmrc=x.compose.yaml"}}
	if err := k.Save(filepath.Join(t.TempDir(), "k.json")); err == nil {
		t.Error("Save accepted it")
	}
}
