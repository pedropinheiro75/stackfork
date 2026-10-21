// Package guard decides whether an agent's shell command would start a
// dev server directly in a stackfork-managed checkout (bypassing the
// isolation `stackfork up` provides). It is a guardrail for cooperative
// agents, not a sandbox: it matches common command shapes and ALLOWS
// whenever it is unsure. It never calls Docker.
package guard

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/argv"
)

// windowsSplit selects Windows command syntax (backslash is a path
// character, not an escape). A var so tests can exercise it on any OS.
var windowsSplit = runtime.GOOS == "windows"

// progBase is the program name of a command word: the last path element
// (either separator on Windows) without a trailing .exe, lower-cased on every
// OS (a case-insensitive filesystem runs STACKFORK too). The .exe
// strip only adds matches, which for a deny guard only adds blocks.
func progBase(w string) string {
	if windowsSplit {
		w = strings.ReplaceAll(w, `\`, "/")
	}
	b := filepath.Base(w)
	if len(b) > 4 && strings.EqualFold(b[len(b)-4:], ".exe") {
		b = b[:len(b)-4]
	}
	return strings.ToLower(b)
}

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
	for _, seg := range guardSegments(command, cwd) {
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
func Segments(command, cwd string) []Segment { return segmentsFor(command, cwd, windowsSplit) }

// guardSegments is what the deny guards look at. On Windows the command may
// run under Git Bash (backslash escapes) or under cmd/PowerShell, so both
// splits count and a block by either wins. Elsewhere it is Segments.
func guardSegments(command, cwd string) []Segment {
	if !windowsSplit {
		return Segments(command, cwd)
	}
	return append(segmentsFor(command, cwd, false), segmentsFor(command, cwd, true)...)
}

func segmentsFor(command, cwd string, windows bool) []Segment {
	var out []Segment
	dir := cwd
	for _, words := range splitCommandsFor(command, windows) {
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
			for _, s := range segmentsFor(inner, dir, windows) {
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

// shellDashC unwraps `bash -c "cmd"` / `sh -lc "cmd"`, and on Windows
// `cmd /c "cmd"` and `powershell -Command "cmd"`.
func shellDashC(words []string) (string, bool) {
	switch progBase(words[0]) {
	case "bash", "sh", "zsh", "dash":
	case "cmd":
		for i := 1; i < len(words); i++ {
			w := strings.ToLower(words[i])
			if len(w) >= 2 && (w[:2] == "/c" || w[:2] == "/k") {
				// "/c" then the command words, or `/c"..."` glued into one word.
				return strings.TrimSpace(words[i][2:] + " " + strings.Join(words[i+1:], " ")), true
			}
		}
		return "", false
	case "powershell", "pwsh":
		return powershellCommand(words[1:])
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

// psFlagIs reports whether w is -name, abbreviated to at least two characters.
func psFlagIs(w, name string) bool {
	w = strings.ToLower(w)
	return len(w) >= 2 && w[0] == '-' && strings.HasPrefix(name, w[1:])
}

// powershellCommand finds the command in powershell's arguments: what follows
// -Command (or any abbreviation, -c included), else the first argument that
// is not a flag.
func powershellCommand(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		w := args[i]
		switch {
		case psFlagIs(w, "command"):
			if i+1 < len(args) {
				return strings.Join(args[i+1:], " "), true
			}
			return "", false
		case psFlagIs(w, "file"):
			return "", false
		case psFlagIs(w, "executionpolicy"), psFlagIs(w, "windowstyle"), psFlagIs(w, "workingdirectory"),
			psFlagIs(w, "inputformat"), psFlagIs(w, "outputformat"), psFlagIs(w, "version"), psFlagIs(w, "configurationname"):
			i++ // takes a value
		case strings.HasPrefix(w, "-"):
		default:
			return strings.Join(args[i:], " "), true
		}
	}
	return "", false
}

// splitCommandsFor tokenizes command into simple commands split on unquoted
// && || ; | & and newlines, honoring single and double quotes and
// backslash escapes. Quotes are removed from the words.

// splitCommandsFor is splitCommands with the syntax chosen: with windows a
// backslash is a literal character (paths), ` and ^ (PowerShell and cmd
// escapes) are dropped keeping the next character, and only quotes group.
func splitCommandsFor(command string, windows bool) [][]string {
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
			} else if !windows && r == '\\' && quote == '"' && i+1 < len(rs) {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case windows && (r == '`' || r == '^') && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case !windows && r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case r == ' ' || r == '\t':
			flushWord()
		case r == '\n' || r == ';' || r == '&' || r == '|' || (!windows && r == '`'):
			flushCmd() // a backtick opens or closes a command substitution
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
		if _, ok := wrappers[progBase(w)]; ok {
			words = skipWrapper(words)
			continue
		}
		break
	}
	return words
}

// wrappers are the words that run the rest of the line as a command, with
// the options that take a separate value (the glued --opt=value form needs no
// help). Their options are skipped so that `env -i op read ...` or `sudo -u U
// ...` is seen as the command it runs.
var wrappers = map[string]map[string]bool{
	"time": nil, "nohup": nil, "command": nil, "setsid": nil, "builtin": nil,
	"env":        {"-u": true, "-C": true, "-S": true, "--unset": true, "--chdir": true},
	"sudo":       {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-T": true, "-U": true, "--user": true, "--group": true},
	"exec":       {"-a": true},
	"nice":       {"-n": true, "--adjustment": true},
	"ionice":     {"-c": true, "-n": true, "-p": true},
	"arch":       {"-arch": true, "-e": true},
	"stdbuf":     {"-i": true, "-o": true, "-e": true, "--input": true, "--output": true, "--error": true},
	"caffeinate": {"-t": true, "-w": true},
	"timeout":    {"-s": true, "-k": true, "--signal": true, "--kill-after": true},
	"xargs":      {"-n": true, "-I": true, "-P": true, "-d": true, "-s": true, "-L": true, "-E": true, "-a": true, "-i": true},
}

// skipWrapper drops a wrapper word, its options and, for timeout, the
// duration.
func skipWrapper(words []string) []string {
	w := progBase(words[0])
	vf := wrappers[w]
	words = words[1:]
	for len(words) > 0 && strings.HasPrefix(words[0], "-") {
		f := words[0]
		words = words[1:]
		if f == "--" {
			break
		}
		if w == "env" {
			// -S / --split-string: the value is a command line to split again.
			val, ok := "", false
			switch {
			case f == "-S" || f == "--split-string":
				if len(words) > 0 {
					val, ok, words = words[0], true, words[1:]
				}
			case strings.HasPrefix(f, "--split-string="):
				val, ok = strings.TrimPrefix(f, "--split-string="), true
			case strings.HasPrefix(f, "-S"):
				val, ok = f[2:], true
			}
			if ok {
				var split []string
				for _, c := range splitCommandsFor(val, windowsSplit) {
					split = append(split, c...)
				}
				return append(split, words...)
			}
		}
		if vf[f] && len(words) > 0 {
			words = words[1:]
		}
	}
	if w == "timeout" && len(words) > 0 {
		words = words[1:]
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
	switch progBase(words[0]) {
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
	switch progBase(rest[0]) {
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
	if progBase(words[0]) == "docker" {
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

// stackforkCall is the action and the action's arguments of a stackfork
// command line, split the way the CLI splits it (internal/argv): single-dash
// global flags, flags glued with =, and an action word that is not first.
func stackforkCall(w []string) (action string, args []string) {
	_, action, args, _ = argv.Split(w[1:])
	return action, args
}

// Destructive reports whether command runs `stackfork prune` with --yes/-y or
// --all, or `stackfork gc --apply`: the deleting forms. A dry run is not.
// Unlike Decide this applies in any directory.
func Destructive(command, cwd string) bool {
	if bypassesConfirmation(command) || secretForbidden(command, cwd) || liveForbidden(command, cwd) {
		return true
	}
	for _, seg := range guardSegments(command, cwd) {
		w := seg.Words
		if progBase(w[0]) != "stackfork" {
			continue
		}
		action, args := stackforkCall(w)
		for _, a := range args {
			a = strings.TrimLeft(a, "-")
			name, _, _ := strings.Cut(a, "=")
			switch {
			case action == "prune" && (name == "yes" || name == "y" || name == "all"):
				return true
			case action == "gc" && name == "apply":
				return true
			case action == "plan" && name == "trust": // people trust host steps, agents never
				return true
			case action == "seed" && name == "force":
				return true
			case action == "start-env" && name == "force": // agents learn the project instead
				return true
			}
		}
		if action == "reset" { // deletes the environment's data, whatever the flags
			return true
		}
	}
	return false
}

// bypassesConfirmation catches the ways around the dashboard's native
// confirmation: posting to its run/action endpoints directly, or clicking
// (or typing into) a dialog through System Events.
func bypassesConfirmation(command string) bool {
	// Only an HTTP client calling the endpoints counts: editing source that
	// mentions them (sed, grep, an editor) must stay allowed.
	httpClients := map[string]bool{"curl": true, "wget": true, "http": true, "https": true, "xh": true,
		"python": true, "python3": true, "node": true, "bun": true, "deno": true, "ruby": true, "perl": true}
	segs := guardSegments(command, "")
	client := false
	for _, seg := range segs {
		client = client || httpClients[progBase(seg.Words[0])]
	}
	if client {
		// The URL may sit in another segment (`echo URL | xargs curl`), and the
		// raw text catches what the splitter joined or unquoted differently.
		words := strings.Fields(unquote(command))
		for _, seg := range segs {
			words = append(words, seg.Words...)
		}
		for _, w := range words {
			if hitsEndpoint(w) || hitsEndpoint(unquote(w)) {
				return true
			}
		}
	}
	return strings.Contains(command, "osascript") && strings.Contains(command, "System Events") &&
		(strings.Contains(command, "click") || strings.Contains(command, "keystroke") || strings.Contains(command, "key code"))
}

func unquote(s string) string {
	return strings.NewReplacer(`"`, "", `'`, "", `\`, "").Replace(s)
}

// hitsEndpoint reports whether a word names one of the confirmation-gated
// endpoints, or an /api/ path with shell brace or curl glob characters that
// could expand to one.
func hitsEndpoint(w string) bool {
	if strings.Contains(w, "/api/") && strings.ContainsAny(w, "{}[]") {
		return true
	}
	norm := normalizeURLWord(w)
	for _, ep := range []string{"/api/run", "/api/actions", "/api/learn", "/api/secrets", "/api/live"} {
		if strings.Contains(w, ep) || strings.Contains(norm, ep) {
			return true
		}
	}
	return false
}

// normalizeURLWord is the path the server would route a URL word to: cut at
// the query or fragment, percent-decode (an undecodable escape keeps the
// word as is), and clean away `.`, `..` and `//` segments, so
// /api/x/../%73ecrets/set?a=%zz is /api/secrets/set.
func normalizeURLWord(w string) string {
	if i := strings.IndexAny(w, "?#"); i >= 0 {
		w = w[:i]
	}
	if d, err := url.PathUnescape(w); err == nil {
		w = d
	}
	return path.Clean(strings.ReplaceAll(w, `\`, "/"))
}
