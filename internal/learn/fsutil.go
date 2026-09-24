// Package learn implements `stackfork learn`'s deterministic scan (task
// 13.2-13.4, design.md D10): no LLM, no network, no Docker daemon beyond
// `docker compose config`. It never follows a symlink (Go's directory walk
// doesn't descend into them, and readFileNoSymlink refuses to open one
// directly), never reads outside the checkout root, and never records an
// env/npmrc VALUE - only key names and structure.
package learn

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skipDirs are never descended into: dependency/build/VCS trees too large
// and irrelevant to scan, and never containing project-authored scripts.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".stackfork": true, ".agent-dev": true,
	"vendor": true, "dist": true, "build": true, ".turbo": true, ".next": true,
}

// readFileNoSymlink reads a file, refusing to follow it if the path itself
// is a symlink (os.ReadFile would silently follow it, possibly outside the
// checkout).
func readFileNoSymlink(path string) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func fileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0 && !info.IsDir()
}

// findByPredicate walks root (never following symlinked directories, never
// descending into skipDirs) collecting regular files whose basename matches
// match, and returns paths relative to root.
func findByPredicate(root string, match func(name string) bool) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint: a permission error on one entry shouldn't abort the scan
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if match(d.Name()) {
			rel, err := filepath.Rel(root, path)
			if err == nil {
				found = append(found, rel)
			}
		}
		return nil
	})
	return found
}

func isEntrypointName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, "entrypoint.sh") || lower == "entrypoint" || strings.HasSuffix(lower, "entrypoint.bash")
}
