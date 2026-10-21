package repos

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// now is the clock of the aside folder's name; tests replace it.
var now = time.Now

// FindMainRecipe is FindSiblingRecipe over the main checkout's own instances
// only, so no other worktree is chosen (improve-worktree-recipes D5). It
// returns (nil, nil) when the main checkout has no prepared instance.
func FindMainRecipe(main, target string, insts []inventory.Instance) (*SiblingRecipe, error) {
	var mine []inventory.Instance
	for _, i := range insts {
		if i.Root != "" && sameDir(i.Root, main) {
			mine = append(mine, i)
		}
	}
	r, err := FindSiblingRecipe(main, target, "", mine)
	if r != nil {
		r.skipKnowledge = true
	}
	return r, err
}

// MoveReport lists what MoveAside moved, as slash paths relative to the
// worktree, and the folder they went to (relative to it too).
type MoveReport struct {
	Dir   string
	Moved []string
}

// untrackedIn reports whether git does not track x in root. Only exit
// status 1 of ls-files --error-unmatch means untracked; any other failure
// (a path beyond a symbolic link exits 128) is an error.
func untrackedIn(root, x string) (bool, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", x)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("could not ask git whether %s is tracked: %w", x, err)
}

// adapterDir is the rule recipeFrom uses for a runtime env: only these
// folders hold files stackfork moves aside or copies.
func adapterDir(d string) bool { return d == ".stackfork" || d == ".agent-dev" }

// insideRoot refuses a path (relative to root) that is a symbolic link or
// resolves outside root. A missing path passes.
func insideRoot(root, rel string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; nothing was moved", rel)
	}
	rp, e1 := filepath.EvalSymlinks(p)
	rr, e2 := filepath.EvalSymlinks(root)
	if e1 != nil || e2 != nil {
		return fmt.Errorf("could not resolve %s; nothing was moved", rel)
	}
	if x, err := filepath.Rel(rr, rp); err != nil || x == ".." || strings.HasPrefix(x, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s resolves outside the worktree; nothing was moved", rel)
	}
	return nil
}

// AsideFiles are the files MoveAside would move (D6): the regular files
// directly in each worktree adapter folder (.stackfork or .agent-dev) that
// holds a file of the recipe's untracked list, plus the recipe's env file,
// that git does not track in the worktree. Sorted, slash paths relative to
// root. It refuses an untracked recipe file outside the adapter folders, a
// symbolic-link folder, and any git failure; it never returns a .git file.
func AsideFiles(root string, r *SiblingRecipe) ([]string, error) {
	set := map[string]bool{}
	dirs := map[string]bool{}
	for _, x := range r.untracked {
		d := filepath.ToSlash(filepath.Dir(x))
		if !adapterDir(d) {
			return nil, fmt.Errorf("the main checkout's recipe uses the untracked file %s outside .stackfork and .agent-dev; it cannot be reused here (track it in git, or move it into .stackfork)", x)
		}
		dirs[d] = true
	}
	if err := insideRoot(root, ".stackfork-aside"); err != nil {
		return nil, err
	}
	consider := func(x string) error {
		if filepath.Base(x) == ".git" {
			return nil
		}
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(x)))
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		u, err := untrackedIn(root, x)
		if err != nil {
			return err
		}
		if u {
			set[x] = true
		}
		return nil
	}
	for d := range dirs {
		if err := insideRoot(root, d); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(filepath.Join(root, d))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Type().IsRegular() {
				if err := consider(d + "/" + e.Name()); err != nil {
					return nil, err
				}
			}
		}
	}
	if r.EnvFile != "" {
		if !adapterDir(filepath.ToSlash(filepath.Dir(r.EnvFile))) {
			return nil, fmt.Errorf("the env file %s is outside .stackfork and .agent-dev; it cannot be reused here", r.EnvFile)
		}
		if err := insideRoot(root, filepath.ToSlash(filepath.Dir(r.EnvFile))); err != nil {
			return nil, err
		}
		if err := consider(r.EnvFile); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out, nil
}

// MoveAside renames the AsideFiles of root to
// .stackfork-aside/<UTC time>/<same path> inside root, keeping their mode.
// Nothing is deleted. It stops at the first error (an existing destination
// or a failed rename) and reports what it moved before.
func MoveAside(root string, r *SiblingRecipe) (MoveReport, error) {
	files, err := AsideFiles(root, r)
	if err != nil {
		return MoveReport{}, err
	}
	rep := MoveReport{Dir: ".stackfork-aside/" + now().UTC().Format("20060102T150405Z")}
	for _, x := range files {
		dst := filepath.Join(root, filepath.FromSlash(rep.Dir), filepath.FromSlash(x))
		if _, err := os.Lstat(dst); err == nil {
			return rep, fmt.Errorf("%s already exists; nothing more was moved", filepath.ToSlash(filepath.Join(rep.Dir, x)))
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return rep, err
		}
		if err := os.Rename(filepath.Join(root, filepath.FromSlash(x)), dst); err != nil {
			return rep, fmt.Errorf("could not move %s aside: %w", x, err)
		}
		rep.Moved = append(rep.Moved, x)
	}
	return rep, nil
}
