package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func guardRepo(t *testing.T) (repo string, flags []string) {
	t.Helper()
	repo = initRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, []string{"--state-dir", t.TempDir(), "--legacy-state-dir", t.TempDir()}
}

func TestGuardBlocksAndAllowsPerTool(t *testing.T) {
	t.Setenv("STACKFORK_HOST_RUN", "")
	repo, _ := guardRepo(t)
	state, legacy := t.TempDir(), t.TempDir()
	args := func(tool string) []string { return []string{"--tool", tool} }
	payloads := map[string]string{
		"claude":  `{"session_id":"s","cwd":"` + repo + `","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"bun run dev"}}`,
		"codex":   `{"session_id":"s","turn_id":"t","cwd":"` + repo + `","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"bun run dev"}}`,
		"copilot": `{"timestamp":1,"cwd":"` + repo + `","toolName":"bash","toolArgs":"{\"command\":\"bun run dev\"}"}`,
	}
	for tool, payload := range payloads {
		var out, errBuf bytes.Buffer
		code, err := cmdGuard(strings.NewReader(payload), &out, &errBuf, args(tool), state, legacy)
		if err != nil {
			t.Fatal(err)
		}
		wantCode := 2
		if tool == "copilot" {
			wantCode = 0
			if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
				t.Fatalf("copilot: expected a deny on stdout, got %q", out.String())
			}
		}
		if code != wantCode || !strings.Contains(errBuf.String(), "managed by stackfork; start it with `stackfork up`") ||
			!strings.Contains(errBuf.String(), "stackfork host app -- <your command>") {
			t.Fatalf("%s: code=%d stderr=%q", tool, code, errBuf.String())
		}

		// The same project, an allowed command.
		var out2, err2 bytes.Buffer
		ok := strings.Replace(payload, "bun run dev", "bun test", 1)
		if code, _ := cmdGuard(strings.NewReader(ok), &out2, &err2, args(tool), state, legacy); code != 0 || out2.Len() != 0 || err2.Len() != 0 {
			t.Fatalf("%s: bun test must pass silently: code=%d out=%q err=%q", tool, code, out2.String(), err2.String())
		}
	}
}

func TestGuardAllowsOutsideManagedAndOnMalformed(t *testing.T) {
	t.Setenv("STACKFORK_HOST_RUN", "")
	plain := initRepo(t)
	state, legacy := t.TempDir(), t.TempDir()
	args := []string{}
	var out, errBuf bytes.Buffer
	if code, _ := cmdGuard(strings.NewReader(`{"cwd":"`+plain+`","tool_input":{"command":"npm run dev"}}`), &out, &errBuf, args, state, legacy); code != 0 || errBuf.Len() != 0 {
		t.Fatalf("unmanaged checkout must be allowed: code=%d %q", code, errBuf.String())
	}
	code, _ := cmdGuard(strings.NewReader(`{{{ nope`), &out, &errBuf, args, state, legacy)
	if code != 0 || !strings.Contains(errBuf.String(), "malformed") {
		t.Fatalf("malformed input: want allow + stderr note, got code=%d %q", code, errBuf.String())
	}
}

func TestGuardBinaryAllowPathUnder50ms(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "stackfork")
	build := exec.Command("go", "build", "-o", bin, "github.com/pedropinheiro75/stackfork/cmd/stackfork")
	build.Dir = moduleRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	repo, _ := guardRepo(t)
	run := func(command string) (time.Duration, int) {
		cmd := exec.Command(bin, "--state-dir", t.TempDir(), "--legacy-state-dir", t.TempDir(), "guard")
		cmd.Stdin = strings.NewReader(`{"cwd":"` + repo + `","tool_name":"Bash","tool_input":{"command":"` + command + `"}}`)
		cmd.Env = append(os.Environ(), "STACKFORK_HOST_RUN=", "PATH=/nonexistent") // no docker reachable at all
		start := time.Now()
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return time.Since(start), code
	}
	// Best of a few runs, so a cold start doesn't flake the bound.
	best := time.Hour
	for i := 0; i < 5; i++ {
		d, code := run("git status && bun test")
		if code != 0 {
			t.Fatalf("allowed command exited %d", code)
		}
		if d < best {
			best = d
		}
	}
	if best > 50*time.Millisecond {
		t.Fatalf("allow path took %v, want < 50ms", best)
	}
	if _, code := run("cd . && bun run dev"); code != 2 {
		t.Fatalf("a dev server in a managed checkout must exit 2, got %d", code)
	}
}
