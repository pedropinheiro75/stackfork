package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
)

var lockfileNames = []string{"yarn.lock", "package-lock.json", "pnpm-lock.yaml", "bun.lockb", "bun.lock", "go.sum",
	"Gemfile.lock", "poetry.lock", "uv.lock", "composer.lock", "Cargo.lock"}

// DepsHash is a hash of the dependency lockfiles (yarn.lock, package-lock.json,
// pnpm-lock.yaml, bun.lock[b], go.sum, Gemfile.lock, poetry.lock, uv.lock,
// requirements*.txt, composer.lock, Cargo.lock) in root and in root/dir. An
// `exec once: deps` step runs again when it changes. No lockfile hashes to a
// fixed value, so such a step runs on the first start only.
func DepsHash(root, dir string) string {
	dirs := []string{root}
	if dir != "" && dir != "." {
		dirs = append(dirs, filepath.Join(root, dir))
	}
	var files []string
	for _, d := range dirs {
		for _, n := range lockfileNames {
			files = append(files, filepath.Join(d, n))
		}
		req, _ := filepath.Glob(filepath.Join(d, "requirements*.txt"))
		files = append(files, req...)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, f)
		h.Write([]byte(rel + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
