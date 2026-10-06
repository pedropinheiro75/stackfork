// Package guard decides whether an agent's shell command would start a
// dev server directly in a stackfork-managed checkout (bypassing the
// isolation `stackfork up` provides). It is a guardrail for cooperative
// agents, not a sandbox: it matches common command shapes and ALLOWS
// whenever it is unsure. It never calls Docker.
package guard

import (
	"os"
	"path/filepath"
	"strings"
)

// HostRunEnv is set by `stackfork host` for its child; a command that
// carries it (as an env prefix, or in the guard's own environment) is the
// sanctioned host-run path.
const HostRunEnv = "STACKFORK_HOST_RUN"

// Segment is one simple command and the directory it would run in. Dir is
// "" when it can't be known (a `cd $VAR`, a subshell, ...).
type Segment struct {
	Words []string
	Dir   string
}

// Block message shown to the agent.
const BlockMessage = "stackfork: this checkout is managed by stackfork; start it with `stackfork up` (host-run: `stackfork host app -- <your command>`)"

// Decide reports whether command, run from cwd, starts a dev server or a raw
// `docker compose up/start` directly. managed is consulted only for a
// command that matched, so the allow path does no IO.
func Decide(command, cwd string, managed func(dir string) bool) (block bool) {
	if os.Getenv(HostRunEnv) != "" {
		return false
	}
	for _, seg := range Segments(command, cwd) {
		if seg.Dir == "" || !startsServer(seg.Words) {
			continue
		}
		if managed(seg.Dir) {
			return true
		}
	}
	return false
}

// Segments splits command into simple commands, tracking `cd X` so each one
// knows its directory, and recursing into `sh -c "..."`.
func Segments(command, cwd string) []Segment {
	var out []Segment
	dir := cwd
	for _, words := range splitCommands(command) {
		words = stripPrefixes(words)
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "cd", "pushd":
			dir = changeDir(dir, words)
			continue
		}
		if inner, ok := shellDashC(words); ok {
			for _, s := range Segments(inner, dir) {
				out = append(out, s)
			}
			continue
		}
		out = append(out, Segment{Words: words, Dir: dir})
	}
	return out
}

func changeDir(dir string, words []string) string {
	if dir == "" || len(words) < 2 {
		return ""
	}
	target := words[1]
	if strings.ContainsAny(target, "$`*") || target == "-" {
		return ""
	}
	if target == "~" || strings.HasPrefix(target, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		target = filepath.Join(home, strings.TrimPrefix(target, "~"))
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	return filepath.Clean(target)
}

// shellDashC unwraps `bash -c "cmd"` / `sh -lc "cmd"`.
func shellDashC(words []string) (string, bool) {
	switch filepath.Base(words[0]) {
	case "bash", "sh", "zsh", "dash":
	default:
		return "", false
	}
	for i := 1; i < len(words)-1; i++ {
		w := words[i]
		if strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.Contains(w, "c") {
			return words[i+1], true
		}
	}
	return "", false
}

// splitCommands tokenizes command into simple commands split on unquoted
// && || ; | & and newlines, honoring single and double quotes and
// backslash escapes. Quotes are removed from the words.
func splitCommands(command string) [][]string {
	var cmds [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	flushWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	flushCmd := func() {
		flushWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
			words = nil
		}
	}
	rs := []rune(command)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(rs) {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case r == ' ' || r == '\t':
			flushWord()
		case r == '\n' || r == ';' || r == '&' || r == '|':
			flushCmd()
		case r == '(' || r == ')' || r == '{' || r == '}':
			// Grouping: treat as a separator; the commands inside are
			// still analysed (their cd's leak out, which only ever errs
			// toward the directory the agent most likely meant).
			flushCmd()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flushCmd()
	return cmds
}

// stripPrefixes removes leading VAR=value assignments and transparent
// wrappers (env, time, nohup, ...). A STACKFORK_HOST_RUN assignment turns
// the whole command into the sanctioned path, which is expressed by
// returning `stackfork host`.
func stripPrefixes(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		if name, _, ok := strings.Cut(w, "="); ok && isEnvName(name) {
			if name == HostRunEnv {
				return []string{"stackfork", "host"}
			}
			words = words[1:]
			continue
		}
		switch w {
		case "env", "time", "nohup", "sudo", "command", "exec", "setsid", "builtin":
			words = words[1:]
			continue
		}
		break
	}
	return words
}

func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

var valueFlags = map[string]bool{
	"--filter": true, "-F": true, "--prefix": true, "-C": true, "--cwd": true,
	"--workspace": true, "-w": true, "--dir": true, "--cache": true,
}

// firstNonFlag returns the words after skipping flags (and the values of
// the few known value-taking ones).
func nonFlags(words []string) []string {
	var out []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "-") {
			if valueFlags[w] && !strings.Contains(w, "=") {
				i++
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

func startsServer(words []string) bool {
	switch filepath.Base(words[0]) {
	case "stackfork":
		return false // including `stackfork host ...`, the sanctioned path
	case "bun", "npm", "pnpm", "yarn":
		return pmStartsServer(words)
	case "npx", "bunx", "pnpx":
		return binStartsServer(nonFlags(words[1:]))
	case "nuxt", "nuxi", "vite", "next", "nodemon":
		return binStartsServer(nonFlags(words))
	case "docker", "docker-compose":
		return composeStarts(words)
	}
	return false
}

func pmStartsServer(words []string) bool {
	rest := nonFlags(words[1:])
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case "exec", "dlx", "x":
		return binStartsServer(rest[1:])
	case "run", "run-script":
		rest = rest[1:]
		if len(rest) == 0 {
			return false
		}
	}
	switch rest[0] {
	case "dev", "start", "serve":
		return true
	}
	return false
}

func binStartsServer(rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	switch filepath.Base(rest[0]) {
	case "nodemon":
		return true
	case "vite":
		// `vite build` / `vite preview` are not dev servers.
		return len(rest) == 1 || rest[1] == "dev" || rest[1] == "serve"
	case "nuxt", "nuxi", "next":
		return len(rest) > 1 && rest[1] == "dev"
	}
	return false
}

// composeStarts matches `docker compose ... up|start` and
// `docker-compose ... up|start` on the project's own files; a command that
// names stackfork's own snapshot (compose.json) is left alone.
func composeStarts(words []string) bool {
	args := words[1:]
	if filepath.Base(words[0]) == "docker" {
		if len(args) == 0 || args[0] != "compose" {
			return false
		}
		args = args[1:]
	}
	up := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasSuffix(a, "compose.json") {
			return false
		}
		if !strings.HasPrefix(a, "-") && !up {
			// First non-flag word is the subcommand (flag values like -f X
			// come after a flag, skipped below).
			up = a == "up" || a == "start"
			if !up {
				return false
			}
			continue
		}
		if a == "-f" || a == "--file" || a == "-p" || a == "--project-name" || a == "--env-file" || a == "--profile" || a == "--project-directory" {
			if i+1 < len(args) && strings.HasSuffix(args[i+1], "compose.json") {
				return false
			}
			i++
		}
	}
	return up
}

// DestructiveMessage is shown when an agent tries a prune that deletes.
const DestructiveMessage = "stackfork: destructive; ask the user to run it (a dry run, `stackfork prune`, is fine)"

var globalValueFlags = map[string]bool{
	"--root": true, "--state-dir": true, "--legacy-state-dir": true, "--npmrc": true,
	"--secret": true, "--profile-dir": true, "--instance": true,
}

// Destructive reports whether command runs `stackfork prune` with --yes/-y or
// --all, or `stackfork gc --apply`: the deleting forms. A dry run is not.
// Unlike Decide this applies in any directory.
func Destructive(command, cwd string) bool {
	for _, seg := range Segments(command, cwd) {
		w := seg.Words
		if filepath.Base(w[0]) != "stackfork" {
			continue
		}
		action, args := "", []string(nil)
		for i := 1; i < len(w); i++ {
			if strings.HasPrefix(w[i], "-") {
				if globalValueFlags[w[i]] {
					i++ // its value
				}
				continue
			}
			action, args = w[i], w[i+1:]
			break
		}
		for _, a := range args {
			a = strings.TrimLeft(a, "-")
			name, _, _ := strings.Cut(a, "=")
			switch {
			case action == "prune" && (name == "yes" || name == "y" || name == "all"):
				return true
			case action == "gc" && name == "apply":
				return true
			}
		}
	}
	return false
}
