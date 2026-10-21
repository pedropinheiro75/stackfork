package repos

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	return FindSiblingRecipe(main, target, "", mine)
}

// MoveReport lists what MoveAside moved, as slash paths relative to the
// worktree, and the folder they went to (relative to it too).
type MoveReport struct {
	Dir   string
	Moved []string
}

func (r *SiblingRecipe) untrackedIn(root, x string) bool {
	_, err := gitRun(root, "ls-files", "--error-unmatch", "--", x)
	return err != nil
}

// AsideFiles are the files MoveAside would move (D6): the regular files
// directly in each worktree folder that holds a file of the recipe's
// untracked list, plus the recipe's env file, that git does not track in the
// worktree. Sorted, slash paths relative to root.
func AsideFiles(root string, r *SiblingRecipe) ([]string, error) {
	set := map[string]bool{}
	dirs := map[string]bool{}
	for _, x := range r.untracked {
		dirs[filepath.Dir(x)] = true
	}
	consider := func(x string) {
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(x)))
		if err == nil && fi.Mode().IsRegular() && r.untrackedIn(root, x) {
			set[x] = true
		}
	}
	for d := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, d))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Type().IsRegular() {
				consider(filepath.ToSlash(filepath.Join(d, e.Name())))
			}
		}
	}
	if r.EnvFile != "" {
		consider(r.EnvFile)
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
