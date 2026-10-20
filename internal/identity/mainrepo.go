package identity

import (
	"os"
	"path/filepath"
	"strings"
)

// MainRepoOfWorktree reads a worktree's .git file ("gitdir: <common>/
// worktrees/<name>") and returns the main checkout, or "" for anything
// else (a submodule, a malformed file).
func MainRepoOfWorktree(dir string) string {
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
	if r, err := ResolvePath(main); err == nil {
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
	return MainRepoOfWorktree(dir)
}
