package lifecycle

import (
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

var (
	composeSecretsMu    sync.Mutex
	composeSecretsCache = map[string]map[string]string{}
)

// ComposeSecrets are the secret names the Compose source of files uses and
// cannot supply itself: a service uses them and they have no `file:`,
// `content:` or `external`, and an `environment:` source is unset in this
// process. They need a reference like a declared name. The value is the
// first file's name, for messages. Best effort (nil when Compose cannot
// render); the Compose call is cached by file and env file times.
func ComposeSecrets(root string, files []string, envFile string) map[string]string {
	if len(files) == 0 {
		return nil
	}
	key := root + "\x00" + envFile
	for _, f := range append(append([]string{}, files...), envFile) {
		fi, err := os.Stat(f)
		if f != "" && err == nil {
			key += "\x00" + f + "@" + fi.ModTime().String()
		} else if f != "" {
			return nil // a missing file: prepare reports it
		}
	}
	composeSecretsMu.Lock()
	cands, ok := composeSecretsCache[key]
	composeSecretsMu.Unlock()
	if !ok {
		cands = compose.SourceSecrets(root, files, envFile)
		composeSecretsMu.Lock()
		composeSecretsCache[key] = cands
		composeSecretsMu.Unlock()
	}
	out := map[string]string{}
	for name, env := range cands {
		if env != "" && os.Getenv(env) != "" {
			continue // resolves from the environment already
		}
		out[name] = filepath.Base(files[0])
	}
	return out
}

// RecipeFiles are the Compose files and env file the checkout's own prepared
// instance recorded, else the first adapter Compose file in the checkout.
func RecipeFiles(root, newBase, legacyBase string) (files []string, envFile string) {
	want, _ := filepath.EvalSymlinks(root)
	for _, base := range []string{newBase, legacyBase} {
		paths, _ := filepath.Glob(filepath.Join(base, "*", "manifest.json"))
		for _, p := range paths {
			m, err := state.ReadManifest(p)
			if err != nil || m == nil || len(m.Files) == 0 {
				continue
			}
			if got, _ := filepath.EvalSymlinks(m.Root); got == want {
				if m.EnvFile != nil {
					envFile = *m.EnvFile
				}
				return m.Files, envFile
			}
		}
	}
	for _, dir := range []string{".stackfork", ".agent-dev"} {
		if m, _ := filepath.Glob(filepath.Join(root, dir, "*.compose.yaml")); len(m) > 0 {
			sort.Strings(m)
			return m[:1], ""
		}
	}
	return nil, ""
}

// UsedPlus returns used extended with the names the Compose files use, so a
// folder mapping applies to them (they count as declared).
func UsedPlus(used map[string]string, root string, files []string, envFile string) map[string]string {
	out := ComposeSecrets(root, files, envFile)
	if out == nil {
		out = map[string]string{}
	}
	for n, v := range used {
		out[n] = v
	}
	return out
}
