package dashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
)

// A folder rule hides an agent there (a client policy), an agent that is
// not installed is unavailable, and other folders are unaffected.
func TestAgentChoicesHonourRulesAndInstalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKFORK_CONFIG_DIR", dir)
	client := filepath.Join(dir, "Client")
	os.MkdirAll(filepath.Join(client, "app"), 0o755)
	os.MkdirAll(filepath.Join(dir, "Mine"), 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"roots":[],"agentRules":[{"path":"`+client+`","deny":["claude"],"reason":"client policy"}]}`), 0o600)
	old := LookPath
	defer func() { LookPath = old }()
	LookPath = func(bin string) (string, error) {
		if bin == "copilot" {
			return "", errors.New("missing")
		}
		return "/bin/" + bin, nil
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]agentChoice{}
	app, _ := filepath.EvalSymlinks(filepath.Join(client, "app"))
	for _, c := range agentChoices(cfg, app) {
		got[c.ID] = c
	}
	if got["claude"].Available || got["claude"].Reason != "client policy" || !got["codex"].Available || got["copilot"].Available || got["copilot"].Reason != "not installed" {
		t.Fatalf("%+v", got)
	}
	mine, _ := filepath.EvalSymlinks(filepath.Join(dir, "Mine"))
	for _, c := range agentChoices(cfg, mine) {
		if c.ID == "claude" && !c.Available {
			t.Fatal("rule leaked outside its folder")
		}
	}
	// a saved config keeps the rule
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.json")); !strings.Contains(string(b), "client policy") {
		t.Fatalf("rule lost on save: %s", b)
	}
}
