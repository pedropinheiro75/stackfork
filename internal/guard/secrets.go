package guard

import (
	"os"
	"path/filepath"
	"strings"
)

// SecretMessage is shown when an agent tries to set, map or read a secret.
const SecretMessage = "stackfork: secrets are set, mapped and read by a person; ask the user to run it (agents may run `stackfork secret ls` and `stackfork secret check`)"

// MappedRefs returns the references of the machine map. The CLI sets it from
// config.json; the guard stays free of that package and does no IO until a
// 1Password command matched.
var MappedRefs = func() ([]string, error) { return nil, nil }

// DenyMessage is the message for a command Destructive blocked.
func DenyMessage(command, cwd string) string {
	if secretForbidden(command, cwd) {
		return SecretMessage
	}
	if liveForbidden(command, cwd) {
		return LiveMessage
	}
	return DestructiveMessage
}

// secretForbidden reports the people-only secret forms (design D12): `secret
// set|map|unmap`, a read of a stackfork keychain item through the OS tools,
// and 1Password reads of a mapped reference. A guardrail, not a sandbox.
func secretForbidden(command, cwd string) bool {
	// The host-run assignment sanctions a dev server, never a secret read.
	command = strings.ReplaceAll(command, HostRunEnv+"=", "SF_X=")
	// PowerShell may be unwrapped on Windows only, so look at the whole line.
	if powershellReadsStackfork(command) {
		return true
	}
	for _, seg := range guardSegments(command, cwd) {
		w := seg.Words
		switch progBase(w[0]) {
		case "stackfork":
			if secretWrite(w) {
				return true
			}
		case "security":
			if securityReadsStackfork(w[1:]) {
				return true
			}
		case "secret-tool":
			if secretToolReadsStackfork(w[1:]) {
				return true
			}
		case "cmdkey":
			if cmdkeyListsStackfork(w[1:]) {
				return true
			}
		case "op":
			if opReadsMapped(command, seg) {
				return true
			}
		}
	}
	return false
}

// secretWrite matches `stackfork [global flags] secret set|map|unmap ...`.
func secretWrite(w []string) bool {
	action := -1
	for i := 1; i < len(w); i++ {
		if strings.HasPrefix(w[i], "-") {
			if globalValueFlags[w[i]] {
				i++
			}
			continue
		}
		action = i
		break
	}
	if action < 0 || w[action] != "secret" {
		return false
	}
	for _, a := range w[action+1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a == "set" || a == "map" || a == "unmap"
	}
	return false
}

// securityArgOpts are the options of the security keychain commands that take
// an argument; the others are flags. Letters may be clustered (-gs NAME) and
// an argument may be glued on (-sNAME).
const securityArgOpts = "aCcDGjlstZ"

// securityReadsStackfork matches `security find-generic-password|
// find-internet-password` with -w or -g unless -s names another service (no
// -s may be stackfork too: an item is found by account or label alone),
// `security dump-keychain -d`, and `-i` among security's own options, which
// runs any command from stdin.
func securityReadsStackfork(args []string) bool {
	for i := 0; i < len(args); i++ { // the options before the subcommand
		a := args[i]
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			break
		}
		if strings.Contains(a[1:], "i") {
			return true
		}
		if strings.HasSuffix(a, "p") { // -p PROMPT
			i++
		}
	}
	for i, a := range args {
		switch a {
		case "find-generic-password", "find-internet-password":
			show, service := false, ""
			opts := args[i+1:]
			for j := 0; j < len(opts); j++ {
				o := opts[j]
				if !strings.HasPrefix(o, "-") || strings.HasPrefix(o, "--") || len(o) < 2 {
					continue
				}
				for k := 1; k < len(o); k++ {
					c := o[k]
					switch {
					case c == 'w' || c == 'g':
						show = true
					case strings.IndexByte(securityArgOpts, c) >= 0:
						val := o[k+1:]
						if val == "" && j+1 < len(opts) {
							j++
							val = opts[j]
						}
						if c == 's' {
							service = val
						}
						k = len(o) // the rest of the cluster was the argument
					}
				}
			}
			return show && (service == "" || strings.EqualFold(service, "stackfork"))
		case "dump-keychain":
			for _, o := range args[i+1:] {
				if strings.HasPrefix(o, "-") && !strings.HasPrefix(o, "--") && strings.Contains(o, "d") {
					return true
				}
			}
			return false
		}
	}
	return false
}

// secretToolReadsStackfork matches `secret-tool lookup|search` unless the
// attributes name a service other than stackfork (search prints the secret
// too; with no service attribute the item may be stackfork's).
func secretToolReadsStackfork(args []string) bool {
	if len(args) == 0 || (args[0] != "lookup" && args[0] != "search") {
		return false
	}
	rest := args[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "-") { // --all, --unlock
		rest = rest[1:]
	}
	named := false
	for i := 0; i+1 < len(rest); i += 2 { // attribute value pairs
		if rest[i] == "service" {
			if strings.EqualFold(rest[i+1], "stackfork") {
				return true // any of several service attributes
			}
			named = true
		}
	}
	return !named
}

// cmdkeyListsStackfork matches `cmdkey /list:TARGET` for a stackfork target
// (Windows Credential Manager target stackfork/ITEM; options are any case).
func cmdkeyListsStackfork(args []string) bool {
	for _, a := range args {
		a = strings.ToLower(a)
		if strings.HasPrefix(a, "/list") && strings.Contains(a, "stackfork") {
			return true
		}
	}
	return false
}

// powershellReadsStackfork matches a command line that reads a
// `stackfork/` Credential Manager target: CredRead through advapi32, the
// CredentialManager module, PasswordVault, or cmdkey.
func powershellReadsStackfork(command string) bool {
	text := strings.ToLower(command) // the whole line: pipes and parentheses split the segments
	if !strings.Contains(text, "stackfork/") && !strings.Contains(text, `stackfork\`) {
		return false
	}
	for _, k := range []string{"credread", "storedcredential", "credentialmanager", "passwordvault", "advapi32", "cmdkey"} {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

// opReadsMapped matches `op read|inject|run` that names a reference of the
// machine map (in the command text, so env prefixes and echoed templates
// count, or in the input file it reads), and `op item get --reveal` of a
// mapped item.
func opReadsMapped(command string, seg Segment) bool {
	var refs []string
	mapped, merr := MappedRefs()
	for _, r := range mapped {
		if strings.HasPrefix(strings.ToLower(r), "op://") {
			refs = append(refs, strings.ToLower(r)) // 1Password matches names without case
		}
	}
	if len(refs) == 0 && merr == nil {
		return false
	}
	sub, rest := "", seg.Words[1:]
	for i, a := range seg.Words[1:] { // past global flags such as --account X
		if a == "read" || a == "inject" || a == "run" || a == "item" {
			sub, rest = a, seg.Words[1+i:]
			break
		}
	}
	if merr != nil {
		// The map cannot be read, so no reference is known to be safe: fail closed.
		return sub == "read" || sub == "inject" || sub == "run" || (sub == "item" && len(rest) > 1 && rest[1] == "get")
	}
	switch sub {
	case "read", "inject", "run":
		lower := strings.ToLower(command)
		words := strings.ToLower(strings.Join(seg.Words, " ")) // quotes removed
		for _, r := range refs {
			if strings.Contains(lower, r) || strings.Contains(words, r) {
				return true
			}
		}
		for _, f := range opInputFiles(seg) {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, r := range refs {
				if strings.Contains(strings.ToLower(string(b)), r) {
					return true
				}
			}
		}
	case "item":
		return opItemReveals(rest, refs)
	}
	return false
}

// opInputFiles are the files `op inject -i`, `--in-file` and `op run
// --env-file` read, relative to the segment's directory.
func opInputFiles(seg Segment) []string {
	var files []string
	add := func(f string) {
		if f == "" {
			return
		}
		if !filepath.IsAbs(f) && seg.Dir != "" {
			f = filepath.Join(seg.Dir, f)
		}
		files = append(files, f)
	}
	w := seg.Words
	for i := 1; i < len(w); i++ {
		switch name, val, ok := strings.Cut(w[i], "="); {
		case ok && (name == "--in-file" || name == "--env-file"):
			add(val)
		case w[i] == "-i" || w[i] == "--in-file" || w[i] == "--env-file":
			if i+1 < len(w) {
				add(w[i+1])
			}
		}
	}
	return files
}

// opItemReveals matches what prints an item's concealed fields: `op item get
// ITEM` of a mapped item with --reveal, --fields or --format json, `op item
// get -` (an item from stdin, as in `op item list --format json | op item get
// - --reveal`) with any of those, and `op item list --format json`, which
// feeds that pipe. Best effort: mapped refs are only known by name.
func opItemReveals(args, refs []string) bool {
	if len(args) < 2 || args[0] != "item" {
		return false
	}
	reveal, vault := false, ""
	var words []string
	for i := 2; i < len(args); i++ {
		a := args[i]
		name, val, hasEq := strings.Cut(a, "=")
		switch {
		case a == "--reveal" || name == "--fields":
			reveal = true
		case name == "--format":
			if !hasEq && i+1 < len(args) {
				i++
				val = args[i]
			}
			if strings.EqualFold(val, "json") {
				reveal = true
			}
		case a == "--vault" && i+1 < len(args):
			i++
			vault = args[i]
		case strings.HasPrefix(a, "--vault="):
			vault = strings.TrimPrefix(a, "--vault=")
		case a == "-" || !strings.HasPrefix(a, "-"):
			words = append(words, a)
		}
	}
	if !reveal {
		return false
	}
	switch args[1] {
	case "list":
		return true
	case "get":
	default:
		return false
	}
	for _, w := range words {
		if w == "-" {
			return true
		}
	}
	for _, r := range refs {
		segs := strings.Split(strings.TrimPrefix(r, "op://"), "/")
		if len(segs) < 2 || (vault != "" && !strings.EqualFold(vault, segs[0])) {
			continue
		}
		for _, w := range words {
			if strings.EqualFold(w, segs[1]) {
				return true
			}
		}
	}
	return false
}
