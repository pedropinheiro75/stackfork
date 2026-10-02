package guard

import (
	"encoding/json"
	"io"
	"strings"
)

// Input is what a tool's pre-tool-use hook tells the guard.
type Input struct {
	Command string
	Cwd     string
}

// Tool names accepted by ParseInput and `stackfork guard --tool`.
const (
	ToolClaude  = "claude"
	ToolCodex   = "codex"
	ToolCopilot = "copilot"
)

// ParseInput reads one hook payload. Claude Code PreToolUse and Codex
// pre_tool_use send {tool_name, tool_input:{command}, cwd, ...}; Copilot CLI
// sends {toolName, toolArgs (a JSON string), cwd, ...}. The three are read
// by one tolerant parser so a schema drift in one tool degrades to "no
// command found" (allow), never to a crash. An empty Command means "not a
// shell command".
func ParseInput(r io.Reader) (Input, error) {
	var p struct {
		Cwd        string          `json:"cwd"`
		Command    json.RawMessage `json:"command"`
		ToolInput  json.RawMessage `json:"tool_input"`
		ToolArgs   json.RawMessage `json:"toolArgs"`
		ToolInput2 json.RawMessage `json:"toolInput"`
	}
	data, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return Input{}, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Input{}, err
	}
	in := Input{Cwd: p.Cwd}
	if in.Command = commandValue(p.Command); in.Command == "" {
		for _, raw := range []json.RawMessage{p.ToolInput, p.ToolInput2, p.ToolArgs} {
			if in.Command = commandOfObject(raw); in.Command != "" {
				break
			}
		}
	}
	return in, nil
}

// commandValue reads a command given as a string or as an array of words.
func commandValue(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var words []string
	if json.Unmarshal(raw, &words) == nil {
		return strings.Join(words, " ")
	}
	return ""
}

// commandOfObject reads {"command": ...} or {"cmd": ...}, where the object
// may itself arrive as a JSON string (Copilot's toolArgs).
func commandOfObject(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		raw = json.RawMessage(text)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	for _, k := range []string{"command", "cmd"} {
		if c := commandValue(obj[k]); c != "" {
			return c
		}
	}
	return ""
}
