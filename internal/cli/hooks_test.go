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
	os.WriteFile(settings, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other-plugin x"}]}]}}`), 0o600)

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
	if !strings.Contains(string(data), "other-plugin x") || strings.Contains(string(data), "guard --tool") {
		t.Fatalf("settings after uninstall: %s", data)
	}
	if _, stderr, code := runCLI(t, "hooks", "install", "--tool", "vim"); code == 0 || !strings.Contains(stderr, "invalid --tool") {
		t.Fatalf("bad tool: code=%d %q", code, stderr)
	}
}

func TestHooksCodexTrustGuidance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME"} {
		t.Setenv(v, "")
	}
	out, _, code := runCLI(t, "hooks", "install", "--tool", "codex")
	if code != 0 || !strings.Contains(out, "trust it once in Codex") || !strings.Contains(out, "/hooks") {
		t.Fatalf("install: %q", out)
	}
	out, _, _ = runCLI(t, "hooks", "status", "--tool", "codex")
	if !strings.Contains(out, "installed but untrusted") {
		t.Fatalf("status without a hooks.state entry: %q", out)
	}
	// An unrelated plugin's entry must not count as our approval.
	cfg := filepath.Join(home, ".codex", "config.toml")
	os.WriteFile(cfg, []byte("[hooks.state.\"other@mkt:hooks/codex-hooks.json:pre_tool_use:0:0\"]\ntrusted_hash = \"sha256:x\"\n"), 0o600)
	if out, _, _ = runCLI(t, "hooks", "status", "--tool", "codex"); !strings.Contains(out, "untrusted") {
		t.Fatalf("a plugin entry is not our approval: %q", out)
	}
	// The user approves it: a user-level entry appears (written by Codex, never by us).
	os.WriteFile(cfg, []byte("[hooks.state.\"/h/.codex/hooks.json:pre_tool_use:0:0\"]\ntrusted_hash = \"sha256:x\"\n"), 0o600)
	out, _, _ = runCLI(t, "hooks", "status", "--tool", "codex")
	if strings.Contains(out, "untrusted") || !strings.Contains(out, "codex: installed") {
		t.Fatalf("trusted status: %q", out)
	}
	if out, _, _ = runCLI(t, "hooks", "install", "--tool", "codex"); strings.Contains(out, "trust it once") {
		t.Fatalf("no trust nag once trusted: %q", out)
	}
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), "sha256:x") || strings.Count(string(data), "[hooks.state") != 1 {
		t.Fatalf("config.toml must never be modified: %s", data)
	}
}
