package cli

import (
	"flag"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/argv"
)

// TestBareInvocationPrintsOverviewAndExitsZero covers the real Copilot CLI
// session finding: an agent ran `stackfork --help` to discover commands,
// got "flag: help requested" and exit 1, learned nothing, and fell back to
// running the dev server directly on the host - bypassing isolation
// entirely. Bare `stackfork`, --help, -h and help must all print a usage
// overview to stdout and exit 0.
func TestBareInvocationPrintsOverviewAndExitsZero(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--help"},
		{"-h"},
		{"help"},
	} {
		out, stderr, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("args=%v: expected exit 0, got %d stderr=%s", args, code, stderr)
		}
		if !strings.Contains(out, "Usage: stackfork") || !strings.Contains(out, "prepare") || !strings.Contains(out, "clone-db") {
			t.Fatalf("args=%v: expected a usage overview naming subcommands, got:\n%s", args, out)
		}
		if stderr != "" {
			t.Fatalf("args=%v: expected nothing on stderr, got %q", args, stderr)
		}
	}
}

// TestUnknownActionExitsNonZeroWithOverviewOnStderr: still an error, but
// never a bare "flag: help requested" - the overview goes to stderr
// alongside a message naming what was actually typed.
func TestUnknownActionExitsNonZeroWithOverviewOnStderr(t *testing.T) {
	out, stderr, code := runCLI(t, "frobnicate")
	if code == 0 {
		t.Fatal("expected a non-zero exit for an unknown action")
	}
	if out != "" {
		t.Fatalf("expected nothing on stdout, got %q", out)
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Fatalf("expected the error to name the unknown action, got %q", stderr)
	}
	if !strings.Contains(stderr, "Usage: stackfork") || !strings.Contains(stderr, "prepare") {
		t.Fatalf("expected the overview on stderr too, got %q", stderr)
	}
}

// TestSubcommandHelpExitsZeroWithUsageAndFlags covers every subcommand that
// takes its own flags: `stackfork <cmd> --help`/`-h` must print real usage
// text (never the bare flag package error) and exit 0.
func TestSubcommandHelpExitsZeroWithUsageAndFlags(t *testing.T) {
	cases := []struct {
		args      []string
		wantFlags []string
	}{
		{[]string{"prepare", "--help"}, []string{"-f", "-env-file", "-hostname"}},
		{[]string{"up", "-h"}, []string{"-build", "-wait-timeout"}},
		{[]string{"down", "--help"}, []string{"-volumes", "-instance"}},
		{[]string{"env", "--help"}, []string{"-format", "-with-passwords"}},
		{[]string{"clone-db", "-h"}, []string{"-from", "-from-dump", "-replace"}},
		{[]string{"list", "--help"}, []string{"-json"}},
		{[]string{"logs", "-h"}, []string{"-tail", "-follow"}},
		{[]string{"gc", "--help"}, []string{"-apply", "-volumes", "-yes"}},
		{[]string{"dashboard", "-h"}, []string{"-port", "-open"}},
	}
	for _, c := range cases {
		out, stderr, code := runCLI(t, c.args...)
		if code != 0 {
			t.Fatalf("args=%v: expected exit 0, got %d stderr=%s", c.args, code, stderr)
		}
		if !strings.Contains(out, "Usage: stackfork "+c.args[0]) {
			t.Fatalf("args=%v: expected a Usage line naming %q, got:\n%s", c.args, c.args[0], out)
		}
		for _, f := range c.wantFlags {
			if !strings.Contains(out, f) {
				t.Errorf("args=%v: expected flag %q in help output, got:\n%s", c.args, f, out)
			}
		}
		if !strings.Contains(out, "references/commands.md") {
			t.Errorf("args=%v: expected a pointer to references/commands.md, got:\n%s", c.args, out)
		}
	}
}

// TestFlaglessSubcommandHelpExitsZero covers the subcommands with no flags
// of their own (where they'd otherwise silently misread --help as a
// positional argument - a service name, an instance name, ...).
func TestFlaglessSubcommandHelpExitsZero(t *testing.T) {
	for _, args := range [][]string{
		{"where", "--help"},
		{"status", "-h"},
		{"urls", "--help"},
		{"restart", "-h"},
		{"start", "--help"},
		{"stop", "-h"},
		{"run", "--help"},
		{"exec", "-h"},
		{"route", "--help"},
		{"proxy", "-h"},
		{"learn", "--help"},
	} {
		out, stderr, code := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("args=%v: expected exit 0, got %d stderr=%s", args, code, stderr)
		}
		if !strings.Contains(out, "Usage: stackfork "+args[0]) {
			t.Fatalf("args=%v: expected a Usage line, got:\n%s", args, out)
		}
	}
}

// TestRealFlagErrorPointsAtSubcommandHelp: a genuinely bad flag is still an
// error (never silently allowed), but now names the subcommand and where
// to look, instead of a bare "flag provided but not defined".
func TestRealFlagErrorPointsAtSubcommandHelp(t *testing.T) {
	_, stderr, code := runCLI(t, "prepare", "--force", "--this-flag-does-not-exist")
	if code == 0 {
		t.Fatal("expected a non-zero exit for an unknown flag")
	}
	if !strings.Contains(stderr, "stackfork prepare --help") {
		t.Fatalf("expected the error to point at `stackfork prepare --help`, got %q", stderr)
	}
}

// --- bug A: global flags work anywhere -------------------------------------

// TestGlobalFlagsWorkAnywhere reproduces the real Copilot CLI session's
// exact failing invocations: a global flag (--npmrc) after the subcommand
// name failed with "flag provided but not defined: -npmrc" (only tokens
// before the action word ever reached the top-level FlagSet), and a
// subcommand-specific flag (-f) before the action word broke the top-level
// parse outright the same way.
func TestGlobalFlagsWorkAnywhere(t *testing.T) {
	requireDocker(t)
	root := initRepo(t)
	if err := exec.Command("git", "-C", root, "-c", "user.email=test@example.com", "-c", "user.name=test",
		"commit", "-q", "--allow-empty", "-m", "init").Run(); err != nil {
		t.Fatal(err)
	}
	npmrc := root + "/fixture.npmrc"
	writeFile(t, npmrc, "//npm.pkg.github.com/:_authToken=synthetic\n")
	composePath := root + "/compose.yaml"
	writeFile(t, composePath, fixtureCompose)
	stateDir := t.TempDir()

	// Global flag AFTER the action word.
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"prepare", "--npmrc", npmrc, "-f", composePath)
	if code != 0 {
		t.Fatalf("global flag after action: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, `"result": "prepared`) {
		t.Fatalf("expected a successful prepare, got %s", out)
	}

	// A subcommand-specific flag (-f) BEFORE the action word, interspersed
	// with a global one.
	stateDir2 := t.TempDir()
	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir2,
		"--npmrc", npmrc, "-f", composePath, "prepare")
	if code != 0 {
		t.Fatalf("subcommand flag before action: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, `"result": "prepared`) {
		t.Fatalf("expected a successful prepare, got %s", out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The guard classifies commands with argv.Split; its action table must be the
// CLI's own, in order.
func TestArgvActionsMatchTheCommandTable(t *testing.T) {
	var names []string
	for _, c := range commandSummaries {
		names = append(names, c.name)
	}
	if !slices.Equal(names, argv.Actions) {
		t.Fatalf("commandSummaries %v\nargv.Actions     %v", names, argv.Actions)
	}
}

// argv.Split recognizes a global flag by this table; it must be the top
// FlagSet's own flags.
func TestArgvGlobalFlagsMatchTheTopFlagSet(t *testing.T) {
	top := flag.NewFlagSet("stackfork", flag.ContinueOnError)
	registerTopFlags(top)
	got := map[string]bool{}
	top.VisitAll(func(f *flag.Flag) { got[f.Name] = true })
	if !maps.Equal(got, argv.GlobalFlags) {
		t.Fatalf("top FlagSet %v\nargv.GlobalFlags %v", got, argv.GlobalFlags)
	}
}
