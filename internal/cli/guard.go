// guard.go implements `stackfork guard [--tool claude|codex|copilot]`, the
// pre-tool-use hook handler (agent-guard capability). It reads the tool's
// hook JSON on stdin and blocks only a dev server started directly inside a
// stackfork-managed checkout; everything else, and anything it can't parse,
// is allowed. It never calls Docker.
package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/pedropinheiro75/stackfork/internal/guard"
)

func cmdGuard(stdin io.Reader, stdout, stderr io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	fs := newFlagSet(stdout, "guard")
	tool := fs.String("tool", guard.ToolClaude, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	switch *tool {
	case guard.ToolClaude, guard.ToolCodex, guard.ToolCopilot:
	default:
		return 2, fmt.Errorf("invalid --tool %q; want claude, codex or copilot", *tool)
	}

	in, err := guard.ParseInput(stdin)
	if err != nil {
		// A guard must never wedge an agent: allow, and say why.
		fmt.Fprintf(stderr, "stackfork guard: ignoring malformed hook input (%v); allowing\n", err)
		return 0, nil
	}
	if in.Command == "" || in.Cwd == "" {
		return 0, nil
	}
	if guard.Destructive(in.Command, in.Cwd) {
		return denyGuard(stdout, stderr, *tool, guard.DestructiveMessage), nil
	}
	blocked := guard.Decide(in.Command, in.Cwd, func(dir string) bool {
		return guard.IsManaged(dir, newStateBase, legacyStateBase)
	})
	if !blocked {
		return 0, nil
	}
	return denyGuard(stdout, stderr, *tool, guard.BlockMessage), nil
}

func denyGuard(stdout, stderr io.Writer, tool, msg string) int {
	fmt.Fprintln(stderr, msg)
	if tool == guard.ToolCopilot {
		// Copilot CLI reads its decision from stdout JSON.
		b, _ := json.Marshal(map[string]string{"permissionDecision": "deny", "permissionDecisionReason": msg})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	return 2
}
