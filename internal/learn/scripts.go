package learn

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// scriptRoles is checked in this order (task 13.2/13.4: "dev/migrate/seed/
// reset/test"); the first matching keyword wins, so "db:migrate" is
// "migrate" rather than falling through to nothing.
var scriptRoles = []struct {
	role string
	keys []string
}{
	{"migrate", []string{"migrate"}},
	{"seed", []string{"seed"}},
	{"reset", []string{"reset"}},
	{"test", []string{"test"}},
	{"dev", []string{"dev", "start"}},
}

func scriptRole(name string) (string, bool) {
	lower := strings.ToLower(name)
	for _, r := range scriptRoles {
		for _, key := range r.keys {
			if strings.Contains(lower, key) {
				return r.role, true
			}
		}
	}
	return "", false
}

// readPackageJSONScripts reads package.json's top-level "scripts" object.
// It does not walk into workspace packages: turbo/yarn workspace pipelines
// are invoked from the root scripts, which is what this records.
// ponytail: per-workspace scripts aren't walked; add if a project's real
// dev/migrate/seed commands live only inside a workspace, not the root.
func readPackageJSONScripts(root string) (map[string]string, bool) {
	data, ok := readFileNoSymlink(filepath.Join(root, "package.json"))
	if !ok {
		return nil, false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, false
	}
	return pkg.Scripts, true
}

func detectScripts(root string) []knowledge.Script {
	var scripts []knowledge.Script

	if raw, ok := readPackageJSONScripts(root); ok {
		names := make([]string, 0, len(raw))
		for name := range raw {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if role, ok := scriptRole(name); ok {
				scripts = append(scripts, knowledge.Script{Role: role, Name: name, Source: "package.json", Command: raw[name]})
			}
		}
	}

	scripts = append(scripts, detectMakeTargets(root, "Makefile")...)
	scripts = append(scripts, detectMakeTargets(root, "justfile")...)
	return scripts
}

// makeTarget matches a Makefile/justfile recipe header: a bare name (no
// leading whitespace) followed by a colon. Good enough for name matching;
// it doesn't parse make's full syntax (pattern rules, .PHONY, variables).
var makeTarget = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:($|[^=])`)

func detectMakeTargets(root, filename string) []knowledge.Script {
	data, ok := readFileNoSymlink(filepath.Join(root, filename))
	if !ok {
		return nil
	}
	var scripts []knowledge.Script
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		m := makeTarget.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if name == ".PHONY" {
			continue
		}
		role, ok := scriptRole(name)
		if !ok {
			continue
		}
		command := ""
		if i+1 < len(lines) {
			next := lines[i+1]
			if strings.HasPrefix(next, "\t") || (strings.HasPrefix(next, "    ") && filename == "justfile") {
				command = strings.TrimSpace(next)
			}
		}
		scripts = append(scripts, knowledge.Script{Role: role, Name: name, Source: filename, Command: command})
	}
	return scripts
}

// lineOfKey returns the 1-indexed line number of the first line containing
// needle, or 0 if not found. Used to point risk findings at a source line
// without re-parsing JSON structurally.
func lineOfKey(data []byte, needle string) int {
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}
