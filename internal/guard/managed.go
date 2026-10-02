package guard

import (
	"os"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// checkoutRoot walks up from dir to the nearest directory holding a .git
// (a directory, or the file a linked worktree has). No git subprocess.
func checkoutRoot(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// IsManaged reports whether dir is inside a stackfork-managed checkout: the
// nearest Git checkout has a prepared instance in either state dir, or a
// `.stackfork/` or `.agent-dev/` directory at its root.
func IsManaged(dir, newStateBase, legacyStateBase string) bool {
	root := checkoutRoot(dir)
	if root == "" {
		return false
	}
	for _, marker := range []string{".stackfork", ".agent-dev"} {
		if fi, err := os.Stat(filepath.Join(root, marker)); err == nil && fi.IsDir() {
			return true
		}
	}
	resolved, err := identity.ResolvePath(root)
	if err != nil {
		resolved = root
	}
	for _, e := range []struct{ base, prefix string }{
		{newStateBase, identity.Prefix}, {legacyStateBase, identity.LegacyPrefix},
	} {
		matches, _ := state.Manifests(e.base, e.prefix)
		for _, m := range matches {
			info, err := state.ReadManifest(m)
			if err == nil && info != nil && (info.Root == resolved || info.Root == root) {
				return true
			}
		}
	}
	return false
}
