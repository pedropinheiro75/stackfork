package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Real sample payloads, one per tool.
const (
	claudePayload  = `{"session_id":"abc123","transcript_path":"/Users/x/.claude/projects/p/abc123.jsonl","cwd":"/repo","permission_mode":"default","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"%s","description":"start the app"},"tool_use_id":"toolu_01"}`
	codexPayload   = `{"session_id":"019a","turn_id":"turn-1","transcript_path":null,"cwd":"/repo","hook_event_name":"PreToolUse","model":"gpt-5-codex","tool_name":"Bash","tool_input":{"command":"%s"},"tool_use_id":"call_1"}`
	copilotPayload = `{"timestamp":1704614600000,"cwd":"/repo","toolName":"bash","toolArgs":"{\"command\":\"%s\",\"description\":\"start\"}"}`
)

func payloads(command string) map[string]string {
	esc := strings.ReplaceAll(command, `"`, `\"`)
	return map[string]string{
		ToolClaude:  strings.Replace(claudePayload, "%s", esc, 1),
		ToolCodex:   strings.Replace(codexPayload, "%s", esc, 1),
		ToolCopilot: strings.Replace(copilotPayload, "%s", strings.ReplaceAll(esc, `\`, `\\\`), 1),
	}
}

func TestDecideTable(t *testing.T) {
	t.Setenv(HostRunEnv, "")
	cases := []struct {
		cmd   string
		block bool
	}{
		{"bun run dev", true},
		{"bun dev", true},
		{"npm run dev", true},
		{"npm start", true},
		{"pnpm dev", true},
		{"pnpm --filter web dev", true},
		{"pnpm run serve", true},
		{"yarn dev", true},
		{"yarn start", true},
		{"npx nuxt dev", true},
		{"bunx nuxt dev --port 3000", true},
		{"nuxi dev", true},
		{"npx vite", true},
		{"vite --host", true},
		{"next dev", true},
		{"npx next dev -p 3001", true},
		{"nodemon server.js", true},
		{"docker compose up", true},
		{"docker compose up -d --build", true},
		{"docker compose -f compose.yaml up -d", true},
		{"docker-compose up", true},
		{"docker compose start web", true},
		// chaining, wrappers, env prefixes
		{"cd .worktrees/pr630 && bun run dev", true},
		{"cd sub; npm run dev", true},
		{"PORT=3000 bun run dev", true},
		{"NODE_ENV=development env bun run dev", true},
		{"time npm start", true},
		{"git pull && bun run dev", true},
		{"bun install | tee x; bun run dev", true},
		{"bash -c 'cd app && bun run dev'", true},
		{`sh -lc "npm run dev"`, true},
		{"(cd app && yarn dev)", true},
		// never blocked
		{"bun test", false},
		{"bun run test", false},
		{"bun run build", false},
		{"npm run lint", false},
		{"npm install", false},
		{"pnpm typecheck", false},
		{"yarn build", false},
		{"vite build", false},
		{"vite preview", false},
		{"next build", false},
		{"nuxi build", false},
		{"git status", false},
		{"git log --oneline -5", false},
		{"echo bun run dev", false},
		{"grep -rn 'npm run dev' README.md", false},
		{"docker compose ps", false},
		{"docker compose logs web", false},
		{"docker compose down", false},
		{"docker compose -p sf-x -f /state/sf-x/compose.json up -d", false},
		{"docker ps", false},
		{"stackfork up", false},
		{"stackfork host app -- bun run dev", false},
		{"STACKFORK_HOST_RUN=1 bun run dev", false},
		{"cd $WORKDIR && bun run dev", false}, // unknown directory: unsure, allow
		{"", false},
		{"bun run 'unterminated", false},
	}
	for _, tool := range []string{ToolClaude, ToolCodex, ToolCopilot} {
		for _, c := range cases {
			raw := payloads(c.cmd)[tool]
			in, err := ParseInput(strings.NewReader(raw))
			if err != nil {
				t.Fatalf("%s: parse %q: %v", tool, raw, err)
			}
			if in.Command != c.cmd || in.Cwd != "/repo" {
				t.Fatalf("%s: parsed %+v from %s", tool, in, raw)
			}
			got := Decide(in.Command, in.Cwd, func(string) bool { return true })
			if got != c.block {
				t.Errorf("%s: Decide(%q) = %v, want %v", tool, c.cmd, got, c.block)
			}
		}
	}
}

func TestNotManagedNeverBlocks(t *testing.T) {
	t.Setenv(HostRunEnv, "")
	if Decide("bun run dev", "/repo", func(string) bool { return false }) {
		t.Fatal("must allow outside a managed checkout")
	}
}

func TestHostRunEnvAllows(t *testing.T) {
	t.Setenv(HostRunEnv, "1")
	if Decide("bun run dev", "/repo", func(string) bool { return true }) {
		t.Fatal("the sanctioned host-run marker must allow")
	}
}

func TestCdTracksDirectory(t *testing.T) {
	t.Setenv(HostRunEnv, "")
	var seen string
	Decide("cd .worktrees/pr630 && bun run dev", "/repo", func(d string) bool { seen = d; return true })
	if seen != "/repo/.worktrees/pr630" {
		t.Fatalf("managed() saw %q", seen)
	}
}

func TestParseInputToleratesOtherShapes(t *testing.T) {
	for _, raw := range []string{
		`{"cwd":"/r","tool_name":"Read","tool_input":{"file_path":"/x"}}`,
		`{"cwd":"/r","toolName":"view","toolArgs":"{\"path\":\"/x\"}"}`,
		`{"cwd":"/r"}`,
		`{"cwd":"/r","tool_input":{"command":["bash","-lc","git status"]}}`,
	} {
		in, err := ParseInput(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if strings.Contains(raw, "bash") {
			if in.Command != "bash -lc git status" {
				t.Fatalf("array command: %q", in.Command)
			}
		} else if in.Command != "" {
			t.Fatalf("%s: expected no command, got %q", raw, in.Command)
		}
	}
	if _, err := ParseInput(strings.NewReader("not json")); err == nil {
		t.Fatal("malformed input must error")
	}
}

func TestIsManaged(t *testing.T) {
	base := t.TempDir()
	mk := func(name string, markers ...string) string {
		root := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, m := range markers {
			if err := os.MkdirAll(filepath.Join(root, m), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	plain := mk("plain")
	agentDev := mk("agentdev", ".agent-dev")
	stackfork := mk("sf", ".stackfork")
	prepared := mk("prepared")
	nonGit := filepath.Join(base, "nongit")
	os.MkdirAll(nonGit, 0o755)

	newBase, legacy := t.TempDir(), t.TempDir()
	inst := filepath.Join(newBase, "sf-prepared-abc")
	os.MkdirAll(inst, 0o700)
	resolved, _ := filepath.EvalSymlinks(prepared)
	if err := state.WritePrivate(filepath.Join(inst, "manifest.json"), &state.Manifest{Project: "sf-prepared-abc", Root: resolved, Branch: "main"}); err != nil {
		t.Fatal(err)
	}

	for dir, want := range map[string]bool{
		plain: false, filepath.Join(plain, "src"): false,
		agentDev: true, filepath.Join(agentDev, "src"): true,
		stackfork: true,
		prepared:  true, filepath.Join(prepared, "src"): true,
		nonGit: false,
	} {
		if got := IsManaged(dir, newBase, legacy); got != want {
			t.Errorf("IsManaged(%s) = %v, want %v", dir, got, want)
		}
	}
}

func TestAllowPathIsFast(t *testing.T) {
	t.Setenv(HostRunEnv, "")
	start := time.Now()
	for i := 0; i < 200; i++ {
		Decide("git status && bun test && npm run lint", "/repo", func(string) bool { t.Fatal("managed() must not run for an allowed command"); return false })
	}
	if per := time.Since(start) / 200; per > 5*time.Millisecond {
		t.Fatalf("allow path too slow: %v per call", per)
	}
}

func TestDestructiveTable(t *testing.T) {
	for cmd, want := range map[string]bool{
		"stackfork prune --stopped --yes":                 true,
		"stackfork prune --yes --stopped":                 true,
		"stackfork prune -y --orphaned":                   true,
		"stackfork prune --all":                           true,
		"stackfork prune --all --json":                    true,
		"stackfork prune --yes=true --stopped":            true,
		"stackfork --state-dir /x prune --stopped --yes":  true,
		"cd /tmp && stackfork prune --orphaned --yes":     true,
		"/usr/local/bin/stackfork prune --stopped --yes":  true,
		"bash -c 'stackfork prune --stopped --yes'":       true,
		"STACKFORK_X=1 stackfork prune --all":             true,
		"stackfork gc --apply":                            true,
		"stackfork plan --trust":                          true,
		"stackfork plan":                                  false,
		"stackfork plan --json":                           false,
		"stackfork gc --apply --volumes --yes":            true,
		"stackfork prune":                                 false,
		"stackfork prune --stopped":                       false,
		"stackfork prune --orphaned --older-than 7d":      false,
		"stackfork prune --stopped --keep-volumes --json": false,
		"stackfork --instance x list --yes":               false,
		"stackfork gc":                                    false,
		"stackfork down --volumes":                        false,
		"echo stackfork prune --yes":                      false,
		"grep -r 'prune --all' README.md":                 false,
		"git prune --all":                                 false,
		"docker system prune --all":                       false,
	} {
		if got := Destructive(cmd, "/repo"); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestSeedForceIsDestructiveButSeedIsNot(t *testing.T) {
	if Destructive("stackfork seed", "/tmp") {
		t.Error("plain seed must be allowed")
	}
	if !Destructive("stackfork seed --force", "/tmp") {
		t.Error("seed --force must be blocked")
	}
	if Destructive("stackfork start-env .worktrees/x", "/tmp") || !Destructive("stackfork start-env --force .worktrees/x", "/tmp") {
		t.Error("start-env --force must be blocked, plain start-env allowed")
	}
}

// An agent cannot get around the dashboard's native confirmation.
func TestConfirmationBypassIsDestructive(t *testing.T) {
	for _, c := range []string{
		`curl -s -X POST -H "X-Stackfork-Token: x" http://127.0.0.1:26607/api/run -d '{}'`,
		`curl -X POST http://stackfork.localhost/api/actions`,
		`python3 -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:26607/api/learn')"`,
		`osascript -e 'tell application "System Events" to click button "Run" of window 1'`,
		`osascript -e 'tell application "System Events" to keystroke return'`,
	} {
		if !Destructive(c, "/tmp") {
			t.Errorf("%s must be blocked", c)
		}
	}
	if Destructive(`osascript -e 'display notification "hi"'`, "/tmp") || Destructive("curl http://127.0.0.1:26607/api/instances", "/tmp") ||
		Destructive(`grep -n "/api/run" internal/dashboard/dashboard.go`, "/tmp") || Destructive(`sed -i '' 's|/api/run|x|' f.go`, "/tmp") {
		t.Error("harmless osascript/curl must stay allowed")
	}
}

func TestSplitCommandsWindowsBackslash(t *testing.T) {
	const cmd = `C:\Users\x\stackfork.exe down --volumes`
	got := splitCommandsFor(cmd, true)
	if len(got) != 1 || got[0][0] != `C:\Users\x\stackfork.exe` || len(got[0]) != 3 {
		t.Fatalf("windows split = %q", got)
	}
	if got := splitCommandsFor(cmd, false); got[0][0] != `C:Usersxstackfork.exe` {
		t.Fatalf("unix split changed: %q", got)
	}
}

func TestDestructiveOnWindowsSyntax(t *testing.T) {
	old := windowsSplit
	windowsSplit = true
	t.Cleanup(func() { windowsSplit = old })
	for cmd, want := range map[string]bool{
		`C:\Users\x\stackfork.exe prune --all`:       true,
		`C:\Users\x\STACKFORK.EXE gc --apply`:        true,
		`cmd /c "stackfork prune --all"`:             true,
		`cmd.exe /c stackfork.exe gc --apply`:        true,
		`echo hi; stackfork prune --all`:             true,
		`powershell -Command "stackfork gc --apply"`: true,
		`C:\Users\x\stackfork.exe prune`:             false,
		`C:\Users\x\stackfork.exe down --volumes`:    false,
		`echo C:\bin\stackfork.exe prune --all`:      false,
	} {
		if got := Destructive(cmd, `C:\repo`); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestExeSuffixTightensOnlyOnUnix(t *testing.T) {
	// The `.exe` strip adds blocks (a deny guard), never removes one.
	if !Destructive("/opt/stackfork.exe prune --all", "/repo") {
		t.Error("stackfork.exe prune --all should be destructive")
	}
	if Destructive("/opt/notstackfork.exe prune --all", "/repo") {
		t.Error("other program names must not match")
	}
	if Destructive(`/opt/a\stackfork prune --all`, "/repo") {
		t.Error("on Unix a backslash is not a path separator")
	}
}
