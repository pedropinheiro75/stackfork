package knowledge

import (
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
