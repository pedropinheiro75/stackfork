package inventory

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Gap compares a linked worktree's Compose files with the ones the main
// checkout's last prepare used (improve-worktree-recipes D1, D2). Main and
// Worktree are slash paths relative to their own checkout; Missing is Main
// minus Worktree. A Gap with no Missing file is not a gap.
type Gap struct {
	Missing, Main, Worktree []string
}

// Has reports whether a Compose file of the reference recipe is missing.
func (g Gap) Has() bool { return len(g.Missing) > 0 }

func sameDir(a, b string) bool {
	ra, e1 := filepath.EvalSymlinks(a)
	rb, e2 := filepath.EvalSymlinks(b)
	return e1 == nil && e2 == nil && ra == rb
}

// mainReference is the most recently used prepared instance whose root is
// the main checkout (new or legacy state directory), or nil.
func mainReference(main string, insts []Instance) *Instance {
	var best *Instance
	for i := range insts {
		c := &insts[i]
		if c.Root == "" || len(c.Files) == 0 || !sameDir(c.Root, main) {
			continue
		}
		if best == nil || c.Used.After(best.Used) {
			best = c
		}
	}
	return best
}

// relInside lists the files that sit inside root, as slash paths relative to it.
func relInside(root string, files []string) []string {
	var out []string
	for _, f := range files {
		x, err := filepath.Rel(root, f)
		if err != nil || x == ".." || strings.HasPrefix(x, ".."+string(filepath.Separator)) {
			continue
		}
		out = append(out, filepath.ToSlash(x))
	}
	return out
}

// RecipeGap is the one place that decides whether the worktree at root,
// prepared with files, lacks a Compose file the main checkout's last
// prepare used. Without a reference instance, or in the main checkout
// itself, it returns the zero Gap.
func RecipeGap(main, root string, files []string, insts []Instance) Gap {
	if main == "" || sameDir(main, root) {
		return Gap{}
	}
	ref := mainReference(main, insts)
	if ref == nil {
		return Gap{}
	}
	g := Gap{Main: relInside(main, ref.Files), Worktree: relInside(root, files)}
	have := map[string]bool{}
	for _, f := range g.Worktree {
		have[f] = true
	}
	for _, f := range g.Main {
		if !have[f] {
			g.Missing = append(g.Missing, f)
		}
	}
	sort.Strings(g.Missing)
	return g
}

// Message is the warning every place shows (D2). It is "" without a gap.
func (g Gap) Message(root string) string {
	if !g.Has() {
		return ""
	}
	n := func(l []string) string {
		s := "s"
		if len(l) == 1 {
			s = ""
		}
		return fmt.Sprintf("%d Compose file%s (%s)", len(l), s, strings.Join(l, ", "))
	}
	return fmt.Sprintf("warning: this worktree prepares with %s; the main checkout's last prepare used %d (%s). Missing: %s. To reuse the main checkout's recipe: stackfork --root %s reset --from-main",
		n(g.Worktree), len(g.Main), strings.Join(g.Main, ", "), strings.Join(g.Missing, ", "), root)
}
