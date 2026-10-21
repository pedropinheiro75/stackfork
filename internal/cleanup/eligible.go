package cleanup

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// cacheDirs are the dependency and build-cache folders that `prepare` knows
// (design D18). Ignored files inside them do not block an automatic cleanup,
// unless they hold a git repository. A name counts only at the repository root
// or in a folder with one of manifestFiles (a build context): src/vendor is
// source.
var cacheDirs = []string{"node_modules", ".nuxt", ".next", ".output", "vendor", ".venv", "target"}

var manifestFiles = []string{"package.json", "go.mod", "Cargo.toml", "composer.json", "pyproject.toml", "requirements.txt", "Gemfile", "pom.xml", "build.gradle", "Pipfile"}

// Check is one eligibility question (design D18). It is asked when the
// candidate is found and again immediately before the first cleanup step.
type Check struct {
	Main      string
	Candidate Candidate
	// Result is the detection that made the candidate worth asking about.
	// Forge CLOSED keeps a git-merged branch closed.
	Result Result
	// Auto is true for an automatic cleanup: ignored files then block it.
	Auto bool
	// Adapter is knowledge.json adapter_files: files stackfork copied at
	// `worktree add`. One that equals the main checkout's copy is not work.
	Adapter []string
	// Locked reports why the instance's run lock or the dashboard's action
	// lock is held ("" when both are free). nil: free.
	Locked func() string
	// Others are the branches of other instances prepared in the same
	// worktree. Their containers and volumes are not this cleanup's to
	// remove, and the worktree would orphan them.
	Others []string
}

// Verdict is the answer to a Check.
type Verdict struct {
	State    string   // Merged, Closed or Open, from the re-check
	Via      string   // set when Merged
	Eligible bool     // a cleanup may run now (for Auto: no ignored files either)
	Warning  string   // why not; shown on the worktree row
	Dirty    []string // uncommitted or untracked paths
	Ignored  []string // ignored files outside the cache folders; removing the worktree deletes them
}

// Evaluate re-reads everything from disk, so a call right before acting sees
// an edit made since the candidate was found. It runs only reads.
func Evaluate(c Check) Verdict {
	v := Verdict{State: Open}
	path, branch := c.Candidate.Path, c.Candidate.Branch
	if b, err := git(path, "symbolic-ref", "-q", "--short", "HEAD"); err != nil || b != branch {
		v.Warning = "this worktree no longer has " + branch + " checked out"
		return v
	}
	def := repos.DefaultBranchOf(c.Main)
	now := Classify(c.Main, branch, Target(c.Main))
	v.State, v.Via = now.State, now.Via
	if c.Result.Forge == "CLOSED" && v.State == Merged {
		v.State, v.Via = Closed, ""
		v.Warning = "PR closed without merge"
		return v
	}
	switch {
	case v.State == Merged:
	case c.Result.State == Merged: // merged when found: a commit came after
		v.Warning = "PR merged, but this branch has commits that are not in " + def
		return v
	default:
		v.Warning = "PR closed without merge"
		return v
	}

	if h := c.Candidate.Head; h != "" {
		if cur, err := git(path, "rev-parse", "HEAD"); err != nil || cur != h {
			v.Warning = branch + " has a new commit since it was checked"
			return v
		}
	}
	if len(c.Others) > 0 {
		v.Warning = "PR merged, but this worktree also has an instance of " + strings.Join(c.Others, ", ") + "; clean it up yourself"
		return v
	}
	if sm, err := git(path, "submodule", "status"); err != nil || initializedSubmodule(sm) {
		v.Warning = "PR merged, but git cannot remove a worktree with an initialized submodule; remove it yourself"
		return v
	}
	// Removing the worktree drops its per-worktree refs; they are not
	// reachable from any branch, so nothing above covers them.
	if refs, err := git(path, "for-each-ref", "--format=%(refname)", "refs/worktree", "refs/bisect", "refs/rewritten"); err != nil || refs != "" {
		v.Warning = "PR merged, but this worktree has refs of its own (" + strings.Join(head(strings.Fields(refs), 3), ", ") + "); removing it would drop them"
		return v
	}
	v.Dirty = dirty(c.Main, path, c.Adapter)
	if len(v.Dirty) > 0 {
		v.Warning = "PR merged, but this worktree has uncommitted changes"
		return v
	}
	v.Ignored = ignoredFiles(c.Main, path, c.Adapter)
	if c.Auto && len(v.Ignored) > 0 {
		v.Warning = "PR merged, but this worktree has ignored files (" + strings.Join(head(v.Ignored, 3), ", ") + ")"
		return v
	}
	if c.Locked != nil {
		if why := c.Locked(); why != "" {
			v.Warning = "PR merged, but " + why
			return v
		}
	}
	v.Eligible = true
	return v
}

// initializedSubmodule: a `git submodule status` line that does not start with
// '-' (an uninitialized submodule is only a gitlink and removes fine).
func initializedSubmodule(status string) bool {
	for _, l := range strings.Split(status, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" && l[0] != '-' {
			return true
		}
	}
	return false
}

func head(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(slices.Clone(s[:n]), fmt.Sprintf("+%d more", len(s)-n))
}

// gitRaw is git whose output keeps its whitespace and NULs.
func gitRaw(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}

// dirty lists `git status --porcelain --untracked-files=all` paths, minus
// adapter files that equal the main checkout's copy. A failing git counts as
// dirty: nothing is deleted on a guess.
func dirty(main, wt string, adapter []string) []string {
	out, err := gitRaw(wt, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return []string{"(git status failed)"}
	}
	var paths []string
	ents := strings.Split(string(out), "\x00")
	for i := 0; i < len(ents); i++ {
		e := ents[i]
		if len(e) < 4 {
			continue
		}
		if e[0] == 'R' || e[0] == 'C' || e[1] == 'R' || e[1] == 'C' {
			i++ // the original path follows
		}
		if p := e[3:]; !adapterCopy(main, wt, p, adapter) {
			paths = append(paths, p)
		}
	}
	return paths
}

// ignoredFiles lists ignored entries outside the cache folders and the
// copied adapter files. Fully ignored folders come as one entry.
func ignoredFiles(main, wt string, adapter []string) []string {
	out, err := gitRaw(wt, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return []string{"(git ls-files failed)"}
	}
	var res []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || adapterCopy(main, wt, p, adapter) {
			continue
		}
		if inCacheDir(wt, p) {
			if !holdsRepo(wt, p) {
				continue
			}
			p += " (contains a git repository)"
		}
		res = append(res, p)
	}
	return res
}

// inCacheDir: p is, or is inside, a cache folder at a known root.
func inCacheDir(wt, p string) bool {
	segs := strings.Split(strings.Trim(filepath.ToSlash(p), "/"), "/")
	for i, seg := range segs {
		if !slices.Contains(cacheDirs, seg) {
			continue
		}
		if i == 0 {
			return true
		}
		for _, m := range manifestFiles {
			if _, err := os.Lstat(filepath.Join(wt, filepath.FromSlash(strings.Join(segs[:i], "/")), m)); err == nil {
				return true
			}
		}
		return false
	}
	return false
}

// holdsRepo: p is, or contains, a `.git` entry (a directory, or the file of a
// linked worktree or submodule). Somebody's work may live there.
func holdsRepo(wt, p string) bool {
	abs := filepath.Join(wt, filepath.FromSlash(p))
	if filepath.Base(abs) == ".git" {
		return true
	}
	found := false
	filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			found = true // unreadable: do not guess
			return filepath.SkipAll
		}
		if d.Name() == ".git" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// adapterCopy: p (a file, or a folder with a trailing slash) is under an
// adapter_files entry and every file in it equals the main checkout's copy.
func adapterCopy(main, wt, p string, adapter []string) bool {
	p = filepath.ToSlash(p)
	rel := strings.TrimSuffix(p, "/")
	under := false
	for _, a := range adapter {
		a = filepath.ToSlash(filepath.Clean(a))
		if rel == a || strings.HasPrefix(rel, a+"/") {
			under = true
		}
	}
	if !under {
		return false
	}
	mine := filepath.Join(wt, filepath.FromSlash(rel))
	fi, err := os.Lstat(mine)
	if err != nil {
		return false
	}
	if !fi.IsDir() {
		return sameFileContent(mine, filepath.Join(main, filepath.FromSlash(rel)))
	}
	same := true
	filepath.WalkDir(mine, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !same {
			return err
		}
		if d.IsDir() {
			return nil
		}
		r, _ := filepath.Rel(wt, path)
		same = sameFileContent(path, filepath.Join(main, r))
		return nil
	})
	return same
}

func sameFileContent(a, b string) bool {
	fa, err := os.Lstat(a)
	if err != nil || !fa.Mode().IsRegular() {
		return false
	}
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

// RemoveWorktree is `git worktree remove PATH`, never --force: git refuses a
// worktree with modified or untracked files, which is the last cleanliness
// check before any data is deleted (design D19).
func RemoveWorktree(main, path string) (string, error) {
	return gitOut(main, "worktree", "remove", path)
}

// DeleteBranch is `git branch -d BRANCH`. When git refuses (a squash or
// rebase merge is not an ancestor) and the work is still proven to be in
// target by Classify, it falls back to `git branch -D`.
func DeleteBranch(main, branch, target string) (string, error) {
	out, err := gitOut(main, "branch", "-d", branch)
	if err == nil {
		return out, nil
	}
	if Classify(main, branch, target).State != Merged {
		return out, fmt.Errorf("git branch -d refused and the branch is no longer proven merged: %w", err)
	}
	out2, err := gitOut(main, "branch", "-D", branch)
	return out + out2, err
}

// gitOut runs a git command that writes, with prompts off, and returns its
// combined output.
func gitOut(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Target is the ref a branch must be in: <remote>/<default>.
func Target(main string) string { return Remote(main) + "/" + repos.DefaultBranchOf(main) }
