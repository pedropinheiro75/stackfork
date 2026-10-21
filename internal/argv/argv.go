// Package argv splits stackfork's command line into global flags, the action
// word and the action's own arguments. The CLI dispatches with it and the
// guard classifies with it, so a form the CLI accepts is a form the guard
// sees (a single dash, a flag glued with =, an action word that is not first).
package argv

import "strings"

// GlobalFlags names every flag the top-level FlagSet registers. Split
// recognizes one of these wherever it appears, with one or two dashes.
var GlobalFlags = map[string]bool{
	"root": true, "npmrc": true, "state-dir": true, "legacy-state-dir": true,
	"profile-dir": true, "secret": true, "instance": true,
}

// Actions lists every command word, in the order of the CLI's overview. A
// test in internal/cli pins it to the CLI's own table.
var Actions = []string{
	"prepare", "up", "down", "status", "urls", "where", "list", "run", "exec", "restart", "start", "stop", "logs",
	"learn", "plan", "host", "open", "env", "clone-db", "seed", "runs", "prune", "gc", "guard", "hooks", "proxy",
	"route", "dashboard", "repos", "worktree", "start-env", "reset", "secret", "live", "config", "doctor", "version",
}

// IsAction reports whether tok is a command or one of the three help spellings.
func IsAction(tok string) bool {
	if tok == "help" || tok == "--help" || tok == "-h" {
		return true
	}
	for _, a := range Actions {
		if a == tok {
			return true
		}
	}
	return false
}

// Split separates the global flags from the action word and the action's
// arguments. A global flag counts anywhere in args; the action is the first
// known action word, anywhere. firstUnrecognized is the first token that was
// neither a global flag nor the action, for the "unknown action" error.
func Split(args []string) (global []string, action string, sub []string, firstUnrecognized string) {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if strings.HasPrefix(tok, "-") {
			name, _, hasEq := strings.Cut(strings.TrimLeft(tok, "-"), "=")
			if GlobalFlags[name] {
				global = append(global, tok)
				if !hasEq && i+1 < len(args) {
					i++
					global = append(global, args[i])
				}
				continue
			}
		}
		if action == "" && IsAction(tok) {
			action = tok
			continue
		}
		if firstUnrecognized == "" {
			firstUnrecognized = tok
		}
		sub = append(sub, tok)
	}
	return global, action, sub, firstUnrecognized
}
