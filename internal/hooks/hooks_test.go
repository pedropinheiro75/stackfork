package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const claudeSettings = `{
  "permissions": {"allow": ["Bash(git status)"]},
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "other-plugin start", "timeout": 20}]}],
    "PreToolUse": [
      {"matcher": "^Bash$|^mcp__.+__(read|view|cat)(_file|_files)?$", "hooks": [{"type": "command", "command": "other-plugin hook file-context", "timeout": 30}]}
    ]
  }
}
`

const copilotConfig = `// User settings belong in settings.json.
// This file is managed automatically.
{
  "firstLaunchAt": "2026-07-24T15:09:24.756Z",
  "installedPlugins": [{"name": "superpowers", "enabled": true}],
  "hooks": {"preToolUse": [{"type": "command", "bash": "./audit.sh", "timeoutSec": 10}]}
}
`

func fixture(t *testing.T) (home string, tools map[string]Tool) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("COPILOT_HOME", "")
	tools = map[string]Tool{}
	for _, tool := range Tools(home) {
		tools[tool.Name] = tool
		if !strings.HasPrefix(tool.Path, home) {
			t.Fatalf("test would touch a path outside the temp home: %s", tool.Path)
		}
	}
	write := func(tool, body string) {
		p := tools[tool].Path
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("claude", claudeSettings)
	write("copilot", copilotConfig)
	// codex: no file at all
	return home, tools
}

func parse(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	doc, _, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestInstallIsIdempotentAndPreservesHooks(t *testing.T) {
	_, tools := fixture(t)
	for name, tool := range tools {
		before := map[string]interface{}{}
		if _, err := os.Stat(tool.Path); err == nil {
			before = parse(t, tool.Path)
		}
		cmd := Command("", name)
		changed, backup, err := Install(tool, cmd)
		if err != nil || !changed {
			t.Fatalf("%s: install changed=%v err=%v", name, changed, err)
		}
		if _, existed := before["hooks"]; existed || name != "codex" {
			if backup == "" {
				t.Fatalf("%s: expected a backup of the existing file", name)
			}
			if data, err := os.ReadFile(backup); err != nil || len(data) == 0 {
				t.Fatalf("%s: backup unreadable: %v", name, err)
			}
		}
		again, backup2, err := Install(tool, cmd)
		if err != nil || again || backup2 != "" {
			t.Fatalf("%s: second install must change nothing (changed=%v backup=%q err=%v)", name, again, backup2, err)
		}
		st, _ := Check(tool)
		if !st.Installed || !st.Exists {
			t.Fatalf("%s: status after install: %+v", name, st)
		}
		raw, _ := os.ReadFile(tool.Path)
		if got := strings.Count(string(raw), "guard --tool "+name); got != 1 {
			t.Fatalf("%s: guard hook present %d times:\n%s", name, got, raw)
		}
	}

	// Existing hooks and unrelated settings survive.
	claude, _ := os.ReadFile(tools["claude"].Path)
	for _, keep := range []string{"other-plugin hook file-context", "other-plugin start", "Bash(git status)"} {
		if !strings.Contains(string(claude), keep) {
			t.Errorf("claude config lost %q:\n%s", keep, claude)
		}
	}
	cop, _ := os.ReadFile(tools["copilot"].Path)
	for _, keep := range []string{"// User settings belong in settings.json.", "// This file is managed automatically.", "./audit.sh", "superpowers", "firstLaunchAt"} {
		if !strings.Contains(string(cop), keep) {
			t.Errorf("copilot config lost %q:\n%s", keep, cop)
		}
	}
}

func TestUninstallRestoresOriginalContent(t *testing.T) {
	_, tools := fixture(t)
	for _, name := range []string{"claude", "copilot"} {
		tool := tools[name]
		original := parse(t, tool.Path)
		if _, _, err := Install(tool, Command("/opt/bin/stackfork", name)); err != nil {
			t.Fatal(err)
		}
		changed, _, err := Uninstall(tool)
		if err != nil || !changed {
			t.Fatalf("%s: uninstall changed=%v err=%v", name, changed, err)
		}
		if got := parse(t, tool.Path); !reflect.DeepEqual(got, original) {
			gj, _ := json.Marshal(got)
			oj, _ := json.Marshal(original)
			t.Fatalf("%s: uninstall did not restore the original content:\n got %s\nwant %s", name, gj, oj)
		}
		if again, _, _ := Uninstall(tool); again {
			t.Fatalf("%s: second uninstall must change nothing", name)
		}
	}
	// Codex had no file: install creates it, uninstall leaves no hooks behind.
	codex := tools["codex"]
	if changed, _, _ := Uninstall(codex); changed {
		t.Fatal("uninstall of a missing file must change nothing")
	}
	Install(codex, Command("", "codex"))
	Uninstall(codex)
	if doc := parse(t, codex.Path); len(doc) != 0 {
		t.Fatalf("expected an empty config after uninstall, got %v", doc)
	}
}

func TestInvalidJSONIsLeftUntouched(t *testing.T) {
	_, tools := fixture(t)
	p := tools["claude"].Path
	os.WriteFile(p, []byte("{ not json"), 0o600)
	if _, _, err := Install(tools["claude"], "stackfork guard --tool claude"); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
	if data, _ := os.ReadFile(p); string(data) != "{ not json" {
		t.Fatalf("file was modified: %q", data)
	}
}

func TestToolPathsHonorEnvOverrides(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/x/claude")
	t.Setenv("CODEX_HOME", "/x/codex")
	t.Setenv("COPILOT_HOME", "/x/copilot")
	got := map[string]string{}
	for _, tool := range Tools("/home/u") {
		got[tool.Name] = tool.Path
	}
	want := map[string]string{"claude": filepath.FromSlash("/x/claude/settings.json"), "codex": filepath.FromSlash("/x/codex/hooks.json"), "copilot": filepath.FromSlash("/x/copilot/settings.json")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Copilot CLI rewrites config.json at session end and drops hooks it did not
// write, so the hook lives in settings.json; an entry an older version put in
// config.json is removed on install and uninstall.
func TestCopilotUsesSettingsAndMigratesLegacy(t *testing.T) {
	_, tools := fixture(t)
	tool := tools["copilot"]
	if filepath.Base(tool.Path) != "settings.json" || len(tool.Legacy) != 1 || filepath.Base(tool.Legacy[0]) != "config.json" {
		t.Fatalf("unexpected copilot paths: %+v", tool)
	}
	legacy := tool.Legacy[0]
	os.WriteFile(legacy, []byte(`{"keep":1,"hooks":{"preToolUse":[{"type":"command","bash":"/x/stackfork guard --tool copilot"},{"type":"command","bash":"./audit.sh"}]}}`), 0o600)
	if _, _, err := Install(tool, Command("", "copilot")); err != nil {
		t.Fatal(err)
	}
	if st, _ := Check(tool); !st.Installed {
		t.Fatal("installed in settings.json")
	}
	raw, _ := os.ReadFile(legacy)
	if strings.Contains(string(raw), "guard --tool") || !strings.Contains(string(raw), "audit.sh") || !strings.Contains(string(raw), `"keep"`) {
		t.Fatalf("legacy entry should be gone and the rest kept: %s", raw)
	}
	if _, _, err := Uninstall(tool); err != nil {
		t.Fatal(err)
	}
	if st, _ := Check(tool); st.Installed {
		t.Fatal("uninstalled")
	}
}
