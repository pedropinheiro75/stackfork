package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestCommandsMdMatchesRealFlagSets verifies references/commands.md's own
// "Flags: `--x`, `--y`" line for every command that has one against that
// command's real --help output, so the cheat sheet can't silently drift
// from the actual flag.FlagSets - a doc claiming a flag stackfork doesn't
// have (or missing one it does) would be caught here, not discovered by a
// user/agent following it.
func TestCommandsMdMatchesRealFlagSets(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve this test file's own path")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	md, err := os.ReadFile(filepath.Join(repoRoot, "references", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}

	sectionPattern := regexp.MustCompile(`(?m)^### (\S+)`)
	flagsLinePattern := regexp.MustCompile("(?m)^Flags: (.+)$")
	flagTokenPattern := regexp.MustCompile("`--([a-zA-Z0-9-]+)`")

	sections := sectionPattern.FindAllStringSubmatchIndex(string(md), -1)
	if len(sections) == 0 {
		t.Fatal("expected at least one ### section in commands.md")
	}
	for i, m := range sections {
		name := string(md[m[2]:m[3]])
		end := len(md)
		if i+1 < len(sections) {
			end = sections[i+1][0]
		}
		body := string(md[m[1]:end])

		if commandSummary(name) == "" {
			t.Errorf("commands.md section %q doesn't match any known subcommand in usage.go's commandSummaries", name)
			continue
		}

		fl := flagsLinePattern.FindStringSubmatch(body)
		if fl == nil {
			continue // a flagless command (status, urls, where, ...) - nothing to check
		}
		var docFlags []string
		for _, tok := range flagTokenPattern.FindAllStringSubmatch(fl[1], -1) {
			docFlags = append(docFlags, tok[1])
		}
		if len(docFlags) == 0 {
			t.Errorf("section %q has a Flags: line but no `--flag` tokens parsed out of it: %q", name, fl[1])
			continue
		}

		out, stderr, code := runCLI(t, name, "--help")
		if code != 0 {
			t.Fatalf("%s --help: code=%d stderr=%s", name, code, stderr)
		}
		for _, flagName := range docFlags {
			if !strings.Contains(out, "-"+flagName) {
				t.Errorf("commands.md documents --%s for %q, but `stackfork %s --help` doesn't mention it:\n%s", flagName, name, name, out)
			}
		}
	}
}
