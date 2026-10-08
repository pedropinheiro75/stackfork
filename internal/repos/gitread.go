package repos

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// perRepoTimeout bounds all git reads of one repo (design.md D2).
const perRepoTimeout = 3 * time.Second

// runGit runs a read-only git command. Nothing here ever contacts a remote:
// the only commands used are for-each-ref, worktree list, status,
// symbolic-ref, rev-parse and show-ref.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

// readGit collects the git-derived data of one repo.
func readGit(path string) (gitData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), perRepoTimeout)
	defer cancel()
	var d gitData

	out, err := runGit(ctx, path, "for-each-ref", "refs/heads",
		"--format=%(refname:short)%09%(committerdate:unix)%09%(upstream:short)%09%(upstream:track,nobracket)")
	if err != nil {
		return d, fmt.Errorf("%s: %w", path, err)
	}
	all := map[string]Branch{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			continue
		}
		b := Branch{Name: f[0], Upstream: f[2]}
		if sec, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			b.LastCommit = time.Unix(sec, 0).UTC()
		}
		b.Ahead, b.Behind = parseTrack(f[3])
		all[b.Name] = b
		if b.LastCommit.After(d.LastCommit) {
			d.LastCommit = b.LastCommit
		}
	}

	out, _ = runGit(ctx, path, "worktree", "list", "--porcelain")
	checkedOut := map[string]bool{}
	for i, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		var w Worktree
		bare, prunable := false, false
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				w.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch refs/heads/"):
				w.Branch = strings.TrimPrefix(line, "branch refs/heads/")
			case strings.HasPrefix(line, "HEAD ") && w.Branch == "":
				w.Branch = "detached-" + strings.TrimPrefix(line, "HEAD ")
			case line == "bare":
				bare = true
			case strings.HasPrefix(line, "prunable"):
				prunable = true
			}
		}
		if w.Path == "" || bare || prunable {
			continue
		}
		real, err := filepath.EvalSymlinks(w.Path)
		if err != nil {
			continue // the folder is gone
		}
		w.Path, w.Main = real, i == 0
		if ctx.Err() == nil {
			st, _ := runGit(ctx, w.Path, "status", "--porcelain=v1", "-uno")
			w.Dirty = strings.TrimSpace(st) != ""
		}
		checkedOut[w.Branch] = true
		d.Worktrees = append(d.Worktrees, w)
	}

	for _, b := range all {
		if !checkedOut[b.Name] {
			d.Branches = append(d.Branches, b)
		}
	}
	sortBranches(d.Branches)
	if d.Worktrees == nil {
		d.Worktrees = []Worktree{}
	}
	if d.Branches == nil {
		d.Branches = []Branch{}
	}
	d.DefaultBranch = defaultBranch(ctx, path, all, d.Worktrees)
	return d, nil
}

func sortBranches(bs []Branch) {
	for i := 1; i < len(bs); i++ {
		for j := i; j > 0 && less(bs[j], bs[j-1]); j-- {
			bs[j], bs[j-1] = bs[j-1], bs[j]
		}
	}
}

func less(a, b Branch) bool {
	if !a.LastCommit.Equal(b.LastCommit) {
		return a.LastCommit.After(b.LastCommit)
	}
	return a.Name < b.Name
}

var trackRe = regexp.MustCompile(`(ahead|behind) (\d+)`)

func parseTrack(s string) (ahead, behind int) {
	for _, m := range trackRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[2])
		if m[1] == "ahead" {
			ahead = n
		} else {
			behind = n
		}
	}
	return
}

// defaultBranch: origin/HEAD when set, else main or master, else whatever
// the main checkout has.
func defaultBranch(ctx context.Context, path string, branches map[string]Branch, wts []Worktree) string {
	if out, err := runGit(ctx, path, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); b != "" {
			return b
		}
	}
	for _, n := range []string{"main", "master"} {
		if _, ok := branches[n]; ok {
			return n
		}
	}
	if len(wts) > 0 && !strings.HasPrefix(wts[0].Branch, "detached-") {
		return wts[0].Branch
	}
	return "main"
}
