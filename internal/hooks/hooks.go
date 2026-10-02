// Package hooks installs, removes and reports stackfork's guard hook in each
// agent tool's own config, without disturbing what is already there (another
// plugin's hooks, unrelated settings). Every change is idempotent and the
// file is copied to a timestamped backup first.
//
// ponytail: configs are edited as generic JSON, so object keys come back
// sorted (content intact, order not). Upgrade path: an order-preserving
// JSON edit if anyone minds; the backup always has the original.
package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tool is one supported agent tool.
type Tool struct {
	Name string
	// Path is the config file this tool reads user-level hooks from.
	Path string
	// copilot is its own shape; claude and codex share Claude Code's.
	copilot bool
	// Legacy are older config files an earlier version wrote the hook to;
	// install and uninstall remove it from them.
	Legacy []string
}

// TrustNote is what to tell the user after installing the Codex hook: Codex
// only runs a hook once the user has approved it. stackfork never writes
// that approval (trusted_hash in config.toml) itself.
const TrustNote = "trust it once in Codex (open `codex`, approve the new hook / run /hooks)"

// NeedsTrust reports whether t is Codex and config.toml next to its hooks
// file has no matching [hooks.state."..."] entry for a user-level (non-plugin)
// pre_tool_use hook.
//
// ponytail: the entry key is matched by shape (`hooks.json:pre_tool_use:`, no
// `@plugin`), not by hash or group index, so another trusted user-level hook
// can mask an untrusted one; upgrade path is recomputing Codex's hash.
func NeedsTrust(t Tool) bool {
	if t.Name != "codex" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(t.Path), "config.toml"))
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[hooks.state.") && strings.Contains(line, "hooks.json:pre_tool_use:") && !strings.Contains(line, "@") {
			return false
		}
	}
	return true
}

// Tools resolves each tool's config path: $CLAUDE_CONFIG_DIR, $CODEX_HOME and
// $COPILOT_HOME win when set, else the usual dot-directory under home.
func Tools(home string) []Tool {
	dir := func(env, sub string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return filepath.Join(home, sub)
	}
	return []Tool{
		{Name: "claude", Path: filepath.Join(dir("CLAUDE_CONFIG_DIR", ".claude"), "settings.json")},
		{Name: "codex", Path: filepath.Join(dir("CODEX_HOME", ".codex"), "hooks.json")},
		// settings.json, not config.json: Copilot CLI rewrites config.json
		// when a session ends and drops hooks it did not write itself
		// (observed on 1.0.89), while settings.json is the user-owned file.
		{Name: "copilot", Path: filepath.Join(dir("COPILOT_HOME", ".copilot"), "settings.json"), copilot: true,
			Legacy: []string{filepath.Join(dir("COPILOT_HOME", ".copilot"), "config.json")}},
	}
}

// Command is the hook command line for tool. bin is stackfork's own path
// when known (a hook's PATH is often thinner than a shell's), else "stackfork".
func Command(bin, tool string) string {
	if bin == "" {
		bin = "stackfork"
	}
	return bin + " guard --tool " + tool
}

func isOurs(command string) bool {
	return strings.Contains(command, "stackfork") && strings.Contains(command, "guard --tool")
}

// Status of one tool's config.
type Status struct {
	Tool      string
	Path      string
	Installed bool
	Exists    bool
}

// Check reports whether the guard hook is present.
func Check(t Tool) (Status, error) {
	st := Status{Tool: t.Name, Path: t.Path}
	doc, _, err := load(t.Path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Exists = true
	_, st.Installed = edit(t, doc, false)
	return st, nil
}

// Install adds the guard hook if absent. It reports whether the file changed
// and the backup path (empty when nothing existed to back up).
func Install(t Tool, command string) (changed bool, backup string, err error) {
	changed, backup, err = apply(t, command, true)
	removeLegacy(t)
	return
}

// removeLegacy best-effort drops the hook from files an older version used.
func removeLegacy(t Tool) {
	for _, p := range t.Legacy {
		old := t
		old.Path, old.Legacy = p, nil
		apply(old, "", false) //nolint:errcheck // a stale entry in a file Copilot rewrites anyway
	}
}

// Uninstall removes the guard hook if present.
func Uninstall(t Tool) (changed bool, backup string, err error) {
	changed, backup, err = apply(t, "", false)
	removeLegacy(t)
	return
}

func apply(t Tool, command string, install bool) (bool, string, error) {
	doc, header, err := load(t.Path)
	existed := err == nil
	if errors.Is(err, os.ErrNotExist) {
		if !install {
			return false, "", nil
		}
		doc, header = map[string]interface{}{}, ""
	} else if err != nil {
		return false, "", err
	}
	if install {
		if _, present := edit(t, doc, false); present {
			return false, "", nil
		}
		editInstall(t, doc, command)
	} else {
		if changed, _ := edit(t, doc, true); !changed {
			return false, "", nil
		}
	}
	backup := ""
	if existed {
		if backup, err = backupFile(t.Path); err != nil {
			return false, "", err
		}
	}
	return true, backup, save(t.Path, doc, header)
}

// edit looks for our hook; with remove it also deletes it. Returns
// (changed, present): present is whether it was there before.
func edit(t Tool, doc map[string]interface{}, remove bool) (changed, present bool) {
	hooks, _ := doc["hooks"].(map[string]interface{})
	key, field := "PreToolUse", "command"
	if t.copilot {
		key, field = "preToolUse", "bash"
	}
	groups, _ := hooks[key].([]interface{})
	var keptGroups []interface{}
	for _, g := range groups {
		gm, _ := g.(map[string]interface{})
		if t.copilot {
			if cmd, _ := gm[field].(string); isOurs(cmd) {
				present = true
				continue
			}
			keptGroups = append(keptGroups, g)
			continue
		}
		inner, _ := gm["hooks"].([]interface{})
		var keptInner []interface{}
		for _, h := range inner {
			hm, _ := h.(map[string]interface{})
			if cmd, _ := hm[field].(string); isOurs(cmd) {
				present = true
				continue
			}
			keptInner = append(keptInner, h)
		}
		if len(keptInner) == len(inner) {
			keptGroups = append(keptGroups, g)
		} else if len(keptInner) > 0 {
			gm["hooks"] = keptInner
			keptGroups = append(keptGroups, gm)
		}
	}
	if !remove || !present {
		return false, present
	}
	if len(keptGroups) == 0 {
		delete(hooks, key)
		if len(hooks) == 0 {
			delete(doc, "hooks")
		}
	} else {
		hooks[key] = keptGroups
	}
	return true, true
}

func editInstall(t Tool, doc map[string]interface{}, command string) {
	hooks, _ := doc["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
		doc["hooks"] = hooks
	}
	if t.copilot {
		list, _ := hooks["preToolUse"].([]interface{})
		hooks["preToolUse"] = append(list, map[string]interface{}{
			"type": "command", "bash": command, "timeoutSec": 5,
		})
		return
	}
	list, _ := hooks["PreToolUse"].([]interface{})
	hooks["PreToolUse"] = append(list, map[string]interface{}{
		"matcher": "^Bash$",
		"hooks":   []interface{}{map[string]interface{}{"type": "command", "command": command, "timeout": 5}},
	})
}

// load reads a JSON config, tolerating the leading // comment lines some
// tools (Copilot CLI) put at the top; they are returned as header and
// written back verbatim.
func load(path string) (map[string]interface{}, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var header strings.Builder
	rest := string(data)
	for {
		trimmed := strings.TrimLeft(rest, " \t\r\n")
		if !strings.HasPrefix(trimmed, "//") {
			rest = trimmed
			break
		}
		nl := strings.IndexByte(trimmed, '\n')
		if nl < 0 {
			nl = len(trimmed)
		}
		header.WriteString(trimmed[:nl] + "\n")
		rest = trimmed[nl:]
	}
	doc := map[string]interface{}{}
	if strings.TrimSpace(rest) != "" {
		if err := json.Unmarshal([]byte(rest), &doc); err != nil {
			return nil, "", fmt.Errorf("%s is not valid JSON, left untouched: %w", path, err)
		}
	}
	return doc, header.String(), nil
}

func backupFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	backup := path + ".stackfork-bak-" + time.Now().Format("20060102T150405.000")
	return backup, os.WriteFile(backup, data, mode)
}

func save(path string, doc map[string]interface{}, header string) error {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".stackfork-hooks-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(header + string(out) + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
