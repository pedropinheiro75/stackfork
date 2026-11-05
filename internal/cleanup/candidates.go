// Package cleanup finds merged-branch worktrees and decides whether they are
// safe to remove (add-live-mode D15-D17). It only reads: no function here
// deletes anything.
package cleanup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// gitTimeout bounds every local git read.
const gitTimeout = 30 * time.Second

// git runs git in dir with prompts disabled and returns trimmed stdout.
func git(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes", "GIT_OPTIONAL_LOCKS=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Candidate is a linked worktree that branch cleanup may act on.
type Candidate struct {
	Path     string // resolved worktree path
	Branch   string
	Upstream string // e.g. origin/feature-login (may be gone)
	Head     string // tip commit
}

// Candidates lists the linked worktrees of the repository whose main
// checkout is main. Never a candidate: the main checkout, a worktree on the
// default branch, a detached or locked worktree, a branch also checked out
// in the main checkout, a worktree whose folder is gone, and a branch
// without an upstream (nothing says its work was reviewed). A candidate
// does not need a running instance.
func Candidates(main string) ([]Candidate, error) {
	out, err := git(main, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	def := repos.DefaultBranchOf(main)
	var cands []Candidate
	mainBranch := ""
	for i, block := range strings.Split(out, "\n\n") {
		var path, branch, head string
		locked, detached, prunable := false, false, false
		for _, l := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(l, "worktree "):
				path = filepath.FromSlash(strings.TrimPrefix(l, "worktree ")) // git prints C:/x on Windows
			case strings.HasPrefix(l, "HEAD "):
				head = strings.TrimPrefix(l, "HEAD ")
			case strings.HasPrefix(l, "branch refs/heads/"):
				branch = strings.TrimPrefix(l, "branch refs/heads/")
			case l == "detached":
				detached = true
			case l == "locked" || strings.HasPrefix(l, "locked "):
				locked = true
			case l == "prunable" || strings.HasPrefix(l, "prunable "):
				prunable = true
			}
		}
		if i == 0 {
			mainBranch = branch
			continue
		}
		if path == "" || branch == "" || detached || locked || prunable || branch == def {
			continue
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		cands = append(cands, Candidate{Path: real, Branch: branch, Head: head})
	}
	var keep []Candidate
	for _, c := range cands {
		if c.Branch == mainBranch {
			continue
		}
		up, err := git(main, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+c.Branch)
		if err != nil || up == "" {
			continue
		}
		c.Upstream = up
		keep = append(keep, c)
	}
	return keep, nil
}
