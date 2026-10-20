// Package privatecheck holds TestNoPrivateMaterial (task 3.10, design.md
// D4): a repo-wide walk that fails a denylisted word into any file that
// isn't allowlisted, so private/personal material can't slip into the
// public release.
package privatecheck

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// modulePath is exempt everywhere it appears literally: it is the public
// Go module path, not a leak (design.md D4: "pedropinheiro (except the
// module path and license)").
const modulePath = "github.com/pedropinheiro75/stackfork"

// denylist is the D4 list, verbatim.
var denylist = []string{
	"idv-isolated",
	"ClientCo",
	"clientco",
	"pedropinheiro",
	"~/code",
	"memory-plugin",
	"notes/",
}

// allowedPaths are the only places the denylist may appear (repo-root
// relative, forward-slash separated), per task 3.10:
//   - internal/identity/identity.go: the legacy-compat constants themselves.
//   - CHANGELOG.md: history necessarily names what changed.
//   - LICENSE: the copyright holder's name (design.md D4).
//   - .goreleaser.yaml: like go.mod's module path, GoReleaser needs the
//     real GitHub owner login (task 4.2's release.github/homebrew_casks
//     repository owner), which is the "pedropinheiro" account name.
var allowedFiles = map[string]bool{
	"internal/identity/identity.go": true,
	"CHANGELOG.md":                  true,
	"LICENSE":                       true,
	".goreleaser.yaml":              true,
	"internal/privatecheck/no_private_material_test.go": true, // holds the denylist itself
}

func allowedPath(rel string) bool {
	return allowedFiles[rel]
}

// skippedDir reports repo-relative directories the walk ignores. A top-level
// .worktrees/ holds linked worktrees of this same repo (other branches'
// copies), which are not part of this checkout. .superpowers/ is local,
// git-excluded agent scratch (plans, reports).
func skippedDir(rel string) bool {
	return rel == ".git" || rel == "dist" || rel == ".worktrees" || rel == ".superpowers"
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// internal/privatecheck/no_private_material_test.go -> repo root
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// findViolations scans content line by line, exempting the module path,
// and reports "path:line: denylisted word" for every hit.
func findViolations(path, content string) []string {
	var violations []string
	for i, line := range strings.Split(content, "\n") {
		clean := strings.ReplaceAll(line, modulePath, "")
		for _, term := range denylist {
			if strings.Contains(clean, term) {
				violations = append(violations, fmt.Sprintf("%s:%d: contains denylisted %q", path, i+1, term))
			}
		}
	}
	return violations
}

// TestNoPrivateMaterial walks the whole repo (including dotdirs such as
// .claude/ and .codex/, skipping only .git) and fails on any D4 denylisted
// string outside the allowlisted files/directories.
func TestNoPrivateMaterial(t *testing.T) {
	root := repoRoot(t)
	var violations []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.Name() == ".git" {
			// a linked worktree has a .git FILE, not a directory
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// .git is VCS internals; dist/ is GoReleaser's gitignored build
			// output (task 4.2), never committed, so neither belongs to
			// "the repo" this test scrubs.
			if skippedDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if allowedPath(rel) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !isText(data) {
			return nil
		}
		violations = append(violations, findViolations(rel, string(data))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(violations) > 0 {
		t.Fatalf("private material found (see design.md D4):\n%s", strings.Join(violations, "\n"))
	}
}

// isText is a cheap binary sniff so a stray image/archive under testdata/
// doesn't get decoded as text; every file in this repo is text today, but
// the check should degrade gracefully rather than panic on binary content.
func isText(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	for _, b := range data[:n] {
		if b == 0 {
			return false
		}
	}
	return true
}
