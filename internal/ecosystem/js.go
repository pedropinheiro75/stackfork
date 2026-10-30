package ecosystem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// JS is the JavaScript/TypeScript ecosystem: npm, pnpm, yarn and bun
// projects, Nuxt, Vite and Next included.
type JS struct{}

func (JS) Name() string { return "js" }

func (JS) Detect(root string) bool {
	_, err := os.Stat(filepath.Join(root, "package.json"))
	return err == nil
}

// packageJSONKeys are the only parts of package.json the scan reads
// (scripts pick the dev server, the watcher and migrate/seed commands), so
// they are the only parts that can make knowledge stale. A dependency bump
// is the install step's business (`once: deps` re-runs on a lockfile
// change), not a reason to learn again.
var packageJSONKeys = []string{"scripts", "packageManager", "workspaces"}

// InputHash hashes a canonical JSON of the package.json keys the scan
// uses. A package.json that does not parse falls back to the whole-file
// hash, so a broken file still reads as changed.
func (JS) InputHash(path string) (string, bool, error) {
	if filepath.Base(path) != "package.json" {
		return "", false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", true, err
	}
	var all map[string]any
	if json.Unmarshal(data, &all) != nil {
		h, err := state.SHA256File(path)
		return h, true, err
	}
	used := map[string]any{}
	for _, k := range packageJSONKeys {
		if v, ok := all[k]; ok {
			used[k] = v
		}
	}
	canon, err := json.Marshal(used) // map keys come out sorted
	if err != nil {
		h, err := state.SHA256File(path)
		return h, true, err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), true, nil
}
