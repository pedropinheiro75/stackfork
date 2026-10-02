package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runs against a temp HOME only; the maintainer's real configs are never
// touched (the env overrides are cleared so nothing can point elsewhere).
func TestHooksCommandOnTempHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME"} {
		t.Setenv(v, "")
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	os.WriteFile(settings, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"memory-plugin x"}]}]}}`), 0o600)

	out, stderr, code := runCLI(t, "hooks", "status")
	if code != 0 || !strings.Contains(out, "claude: not installed") || !strings.Contains(out, "codex: not installed (no ") {
		t.Fatalf("status: code=%d out=%q err=%q", code, out, stderr)
	}
	out, stderr, code = runCLI(t, "hooks", "install", "--tool", "claude")
	if code != 0 || !strings.Contains(out, "claude: installed "+settings) || !strings.Contains(out, "backup:") {
		t.Fatalf("install: code=%d out=%q err=%q", code, out, stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Fatal("--tool claude must not touch the other tools")
	}
	out, _, _ = runCLI(t, "hooks", "install", "--tool", "claude")
	if !strings.Contains(out, "already installed") {
		t.Fatalf("second install: %q", out)
	}
	if out, _, _ = runCLI(t, "hooks", "status", "--tool", "claude"); !strings.Contains(out, "claude: installed") {
		t.Fatalf("status: %q", out)
	}
	if out, _, _ = runCLI(t, "hooks", "uninstall", "--tool", "claude"); !strings.Contains(out, "uninstalled") {
		t.Fatalf("uninstall: %q", out)
	}
	data, _ := os.ReadFile(settings)
	if !strings.Contains(string(data), "memory-plugin x") || strings.Contains(string(data), "guard --tool") {
		t.Fatalf("settings after uninstall: %s", data)
	}
	if _, stderr, code := runCLI(t, "hooks", "install", "--tool", "vim"); code == 0 || !strings.Contains(stderr, "invalid --tool") {
		t.Fatalf("bad tool: code=%d %q", code, stderr)
	}
}
