// usage.go: `stackfork`/`--help`/`-h`/`help` print a usage overview and
// exit 0 instead of the bare `flag: help requested` error every
// SetOutput(io.Discard) FlagSet used to produce (a real agent session hit
// this, learned nothing, and fell back to running the dev server directly
// on the host, bypassing isolation entirely). Every subcommand's own
// `--cmd --help` goes through the same newFlagSet/parseFailure pair, so
// none of them has to wire this up itself.
package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// commandSummaries is the one source of truth for the top-level overview
// and references/commands.md's own verification test - order is the
// display order.
var commandSummaries = []struct{ name, summary string }{
	{"prepare", "isolate this checkout+branch's copy of the Compose stack"},
	{"up", "start the prepared stack"},
	{"down", "stop it (data kept unless --volumes)"},
	{"status", "machine-readable state of every service"},
	{"urls", "this instance's URLs"},
	{"where", "resolve the current checkout+branch to its instance"},
	{"list", "every instance across every checkout"},
	{"run", "run a one-off command in a fresh container"},
	{"exec", "run a command in an already-running container"},
	{"restart", "restart one service"},
	{"start", "start one stopped service"},
	{"stop", "stop one running service"},
	{"logs", "recent (or following) logs for a service"},
	{"learn", "scan a project; record/verify what you learned"},
	{"open", "open this instance's app in the browser (--print: just the URL)"},
	{"env", "print this instance's env vars (for a host-run app)"},
	{"clone-db", "seed this instance's database from another instance or a dump"},
	{"gc", "remove state for checkouts/branches that no longer exist"},
	{"guard", "pre-tool-use hook: block host dev servers in a managed checkout"},
	{"proxy", "the shared portless-URL proxy: up/down/status/reload"},
	{"route", "register a host-run app's port with the proxy"},
	{"dashboard", "a local web page listing every instance"},
	{"doctor", "check the local environment the proxy/tool depend on"},
	{"version", "print the version"},
}

// knownGlobalFlags names every flag `run`'s own top-level FlagSet
// registers - splitArgs recognizes one of these wherever it appears in
// args, not just before the action word.
var knownGlobalFlags = map[string]bool{
	"root": true, "npmrc": true, "state-dir": true, "legacy-state-dir": true,
	"profile-dir": true, "secret": true, "instance": true,
}

// isKnownAction reports whether tok is a real command or one of the three
// help spellings - the set splitArgs treats as "this is the action word",
// wherever it appears.
func isKnownAction(tok string) bool {
	if tok == "help" || tok == "--help" || tok == "-h" {
		return true
	}
	return commandSummary(tok) != ""
}

// splitArgs separates stackfork's global flags from the action word and
// its own arguments. A global flag is recognized anywhere in args, not
// just before the action (flag.FlagSet's own Parse stops looking the
// moment it hits the first non-flag token) - so `stackfork prepare --npmrc
// X -f file`, `stackfork --npmrc X -f file prepare` and `stackfork --npmrc
// X prepare -f file` all resolve to the same global/action/subArgs split.
// firstUnrecognized is the first token that was neither a recognized
// global flag nor the (at most one) action word - used only to name what
// the user actually typed in an "unknown action" error; it plays no part
// in dispatch.
func splitArgs(args []string) (globalArgs []string, action string, subArgs []string, firstUnrecognized string) {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if strings.HasPrefix(tok, "-") {
			name, _, hasEq := strings.Cut(strings.TrimLeft(tok, "-"), "=")
			if knownGlobalFlags[name] {
				globalArgs = append(globalArgs, tok)
				if !hasEq && i+1 < len(args) {
					i++
					globalArgs = append(globalArgs, args[i])
				}
				continue
			}
		}
		if action == "" && isKnownAction(tok) {
			action = tok
			continue
		}
		if firstUnrecognized == "" {
			firstUnrecognized = tok
		}
		subArgs = append(subArgs, tok)
	}
	return globalArgs, action, subArgs, firstUnrecognized
}

func commandSummary(name string) string {
	for _, c := range commandSummaries {
		if c.name == name {
			return c.summary
		}
	}
	return ""
}

// printOverview is `stackfork`/`--help`/`-h`/`help`'s own output, and what
// an unknown command gets alongside its own error on stderr - one shared
// text either way.
func printOverview(w io.Writer) {
	fmt.Fprintln(w, "stackfork isolates a Compose stack per checkout+branch, so parallel worktrees/agents never collide on ports, volumes, networks or databases.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: stackfork [global flags] <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, c := range commandSummaries {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	tw.Flush()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags: --root DIR, --state-dir DIR, --legacy-state-dir DIR, --npmrc PATH, --secret NAME=REF, --profile-dir DIR, --instance NAME (anywhere: before or after the command).")
	fmt.Fprintln(w, "Run `stackfork <command> --help` for that command's own flags, or see references/commands.md for the full reference.")
}

// newFlagSet returns name's FlagSet with a real Usage function, so
// `stackfork <name> --help`/`-h` prints actual usage text to stdout and
// exits 0 - every subcommand's flag parsing goes through this instead of
// calling flag.NewFlagSet directly.
func newFlagSet(stdout io.Writer, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Fprintf(stdout, "Usage: stackfork %s [flags]\n", name)
		if s := commandSummary(name); s != "" {
			fmt.Fprintf(stdout, "\n%s\n", s)
		}
		var buf bytes.Buffer
		fs.SetOutput(&buf)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
		if buf.Len() > 0 {
			fmt.Fprintf(stdout, "\nFlags:\n%s", buf.String())
		}
		fmt.Fprintln(stdout, "\nFull reference: references/commands.md, or `stackfork help`.")
	}
	return fs
}

// parseFailure turns a FlagSet's Parse error into (0, nil) for flag.ErrHelp
// (-h/--help - Parse already invoked fs.Usage itself before returning this,
// since newFlagSet installed it; nothing left to print here) or the
// existing generic-but-now-subcommand-pointing wrapped-error shape
// otherwise, so an agent that mistypes a flag is told exactly where to
// look next instead of a bare "flag provided but not defined".
func parseFailure(fs *flag.FlagSet, err error) (int, error) {
	if errors.Is(err, flag.ErrHelp) {
		return 0, nil
	}
	return 2, fmt.Errorf("%s (see `stackfork %s --help`)", err.Error(), fs.Name())
}

// parseFailureErr is parseFailure for a caller that returns only error (not
// (int, error)) - same flag.ErrHelp-vs-generic-failure split, no separate
// exit code to plumb through.
func parseFailureErr(fs *flag.FlagSet, err error) error {
	_, werr := parseFailure(fs, err)
	return werr
}

// isHelpRequest reports whether subArgs opens with a bare -h/--help - used
// by the handful of subcommands with no flags of their own (status, urls,
// where, route, restart, start, stop, run, exec), which otherwise never
// build a FlagSet at all and would silently misread --help as a
// positional argument (a service name, an instance name, ...).
func isHelpRequest(subArgs []string) bool {
	return len(subArgs) > 0 && (subArgs[0] == "-h" || subArgs[0] == "--help")
}

// printFlaglessUsage is isHelpRequest's own output for a subcommand with
// no flags: just the usage line, summary and reference pointer - no empty
// "Flags:" section.
func printFlaglessUsage(stdout io.Writer, name, usage string) {
	fmt.Fprintf(stdout, "Usage: stackfork %s\n", usage)
	if s := commandSummary(name); s != "" {
		fmt.Fprintf(stdout, "\n%s\n", s)
	}
	fmt.Fprintln(stdout, "\nFull reference: references/commands.md, or `stackfork help`.")
}
