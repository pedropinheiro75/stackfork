// Package repos discovers git repositories under the configured roots and
// reads their branches, worktrees and stackfork state, using local git only.
package repos

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// alwaysSkip are directory names never descended into.
var alwaysSkip = map[string]bool{"node_modules": true, "vendor": true, ".worktrees": true, ".git": true}

// Walk returns the main repository paths found under roots (already
// resolved absolute paths). A directory holding a .git directory is a repo;
// one holding a .git file is a worktree and is attributed to its main repo
// (read from the file, no git call), never listed on its own.
// ponytail: a repo is not descended into, so a repo nested inside another
// repo's tree is not found; add a second pass if that ever matters.
func Walk(roots, exclude []string, maxDepth int) []string {
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[e] = true
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, root := range roots {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil // symlinks are not followed: WalkDir reports them as non-dirs
			}
			rel, _ := filepath.Rel(root, path)
			if rel == "." {
				rel = ""
			}
			name := d.Name()
			if rel != "" && (alwaysSkip[name] || skip[name] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			if rel != "" && strings.Count(rel, string(filepath.Separator))+1 > maxDepth {
				return fs.SkipDir
			}
			fi, err := os.Lstat(filepath.Join(path, ".git"))
			switch {
			case err != nil:
				return nil
			case fi.IsDir():
				add(path)
				return fs.SkipDir
			case fi.Mode().IsRegular():
				if main := mainRepoOfWorktree(path); main != "" && underAny(roots, main) && !excludedPath(roots, skip, main) {
					add(main)
				}
				return fs.SkipDir
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

func underAny(roots []string, p string) bool {
	for _, r := range roots {
		if config.Inside(r, p) {
			return true
		}
	}
	return false
}

// excludedPath reports whether any segment of p below its root is excluded.
func excludedPath(roots []string, skip map[string]bool, p string) bool {
	for _, r := range roots {
		if config.Inside(r, p) {
			rel, _ := filepath.Rel(r, p)
			for _, seg := range strings.Split(rel, string(filepath.Separator)) {
				if skip[seg] || alwaysSkip[seg] || strings.HasPrefix(seg, ".") {
					return true
				}
			}
		}
	}
	return false
}

// mainRepoOfWorktree reads a worktree's .git file ("gitdir: <common>/
// worktrees/<name>") and returns the main checkout, or "" for anything
// else (a submodule, a malformed file).
func mainRepoOfWorktree(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	gitdir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	if filepath.Base(filepath.Dir(gitdir)) != "worktrees" {
		return ""
	}
	common := filepath.Dir(filepath.Dir(gitdir))
	if filepath.Base(common) != ".git" {
		return ""
	}
	main := filepath.Dir(common)
	if _, err := os.Stat(filepath.Join(main, ".git")); err != nil {
		return ""
	}
	if r, err := identity.ResolvePath(main); err == nil {
		return r
	}
	return ""
}

// MainRepoOf returns the main checkout for a repo root or a linked
// worktree, or "" when dir is neither.
func MainRepoOf(dir string) string {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	switch {
	case err != nil:
		return ""
	case fi.IsDir():
		return dir
	}
	return mainRepoOfWorktree(dir)
}
