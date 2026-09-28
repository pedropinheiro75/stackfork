package learn

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// underRoot reports whether path is root or a descendant of it, mirroring
// the "never read outside the checkout" rule the same way compose.Isolate
// checks writable binds.
func underRoot(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// renderProject is a throwaway Compose project name for `docker compose
// config`; nothing is ever started under it (mirrors compose.RenderProject).
const renderProject = "sf-learn"

// Scan performs the deterministic, offline project scan (task 13.2-13.4):
// it finds Compose files and renders them (reusing compose.Render), records
// services/DB/ports/volumes/networks/healthchecks/profiles, runs the
// isolation dry run (task 13.3), detects the package manager, runtime
// versions, dev/migrate/seed/reset/test scripts, startup risks and env key
// names, and returns a draft Knowledge. It never starts anything, never
// touches the network, and never reads a value out of an env/npmrc file.
func Scan(root string) (*knowledge.Knowledge, error) {
	root, err := identity.ResolvePath(root)
	if err != nil {
		return nil, err
	}

	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusDraft}
	inputs := map[string]bool{} // absolute paths read, deduped

	composeFiles := findComposeFiles(root)
	k.Facts.ComposeFiles = composeFiles
	for _, f := range composeFiles {
		inputs[filepath.Join(root, f)] = true
	}

	var envFilePaths []string
	if len(composeFiles) > 0 {
		abs := make([]string, len(composeFiles))
		for i, f := range composeFiles {
			abs[i] = filepath.Join(root, f)
		}
		rendered, err := compose.Render(root, abs, "", "")
		if err != nil {
			return nil, err
		}
		k.Facts.Services = servicesFromRendered(rendered)
		// A project's real env_file (e.g. .env) is typically gitignored, so
		// a fresh checkout commonly declares one that doesn't exist yet;
		// that's a fact for the recipe/gotchas step, not a scan failure.
		for _, f := range envFilesUnderRoot(rendered, root) {
			if fileExists(f) {
				envFilePaths = append(envFilePaths, f)
				inputs[f] = true
			}
		}

		findings, err := compose.IsolateDryRun(rendered, root, renderProject, "")
		if err != nil {
			return nil, err
		}
		k.Refusals = findings
	}

	manager, lockfile, others := detectPackageManager(root)
	k.Facts.PackageManager = manager
	k.Facts.Lockfile = lockfile
	k.Facts.OtherManifests = others
	if lockfile != "" {
		inputs[filepath.Join(root, lockfile)] = true
	}
	for _, o := range others {
		inputs[filepath.Join(root, o)] = true
	}
	if fileExists(filepath.Join(root, "package.json")) {
		inputs[filepath.Join(root, "package.json")] = true
	}

	k.Facts.RuntimeVersions = detectRuntimeVersions(root)
	for _, name := range []string{".node-version", ".nvmrc", ".bun-version", ".tool-versions", "go.mod"} {
		if fileExists(filepath.Join(root, name)) {
			inputs[filepath.Join(root, name)] = true
		}
	}

	k.Facts.Scripts = detectScripts(root)
	for _, name := range []string{"Makefile", "justfile"} {
		if fileExists(filepath.Join(root, name)) {
			inputs[filepath.Join(root, name)] = true
		}
	}

	k.Risks = detectRisks(root)
	for _, rel := range findByPredicate(root, isEntrypointName) {
		inputs[filepath.Join(root, rel)] = true
	}

	k.EnvKeys = detectEnvKeys(root, envFilePaths)
	for _, name := range []string{".env.example", ".env.sample"} {
		if fileExists(filepath.Join(root, name)) {
			inputs[filepath.Join(root, name)] = true
		}
	}

	k.Inputs, err = hashInputs(root, inputs)
	if err != nil {
		return nil, err
	}

	return k, nil
}

// envFilesUnderRoot extracts every service's env_file target paths from a
// rendered Compose config, keeping only ones that resolve inside root
// (never read another project's files outside the checkout).
func envFilesUnderRoot(rendered interface{}, root string) []string {
	model, ok := rendered.(map[string]interface{})
	if !ok {
		return nil
	}
	svcMap, ok := model["services"].(map[string]interface{})
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range svcMap {
		service, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		entries, ok := service["env_file"].([]interface{})
		if !ok {
			continue
		}
		for _, e := range entries {
			var p string
			if m, ok := e.(map[string]interface{}); ok {
				p, _ = m["path"].(string)
			} else if s, ok := e.(string); ok {
				p = s
			}
			if p == "" {
				continue
			}
			resolved, err := identity.ResolvePath(p)
			if err != nil || seen[resolved] {
				continue
			}
			if underRoot(resolved, root) {
				seen[resolved] = true
				out = append(out, resolved)
			}
		}
	}
	sort.Strings(out)
	return out
}

// hashInputs hashes each absolute path in paths, but stores it in the
// result relative to root - knowledge.json is committed (design.md D10),
// so an absolute path would both leak a home directory and be wrong on
// every other clone.
func hashInputs(root string, paths map[string]bool) ([]knowledge.Input, error) {
	list := make([]string, 0, len(paths))
	for p := range paths {
		list = append(list, p)
	}
	sort.Strings(list)

	inputs := make([]knowledge.Input, 0, len(list))
	for _, p := range list {
		sum, err := state.SHA256File(p)
		if err != nil {
			return nil, fmt.Errorf("hashing %s: %w", p, err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		inputs = append(inputs, knowledge.Input{Path: filepath.ToSlash(rel), SHA256: sum})
	}
	return inputs, nil
}

// WriteKnowledge writes .stackfork/knowledge.json, .stackfork/knowledge.md
// and .stackfork/.gitignore under root (task 13.2).
func WriteKnowledge(root string, k *knowledge.Knowledge) error {
	dir := filepath.Join(root, ".stackfork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := k.Save(filepath.Join(dir, "knowledge.json")); err != nil {
		return err
	}
	if err := k.SaveMarkdown(filepath.Join(dir, "knowledge.md")); err != nil {
		return err
	}
	// Knowledge and adapters are meant to be committed (design.md D10); only
	// files that can hold a real value are excluded.
	gitignore := "runtime.env\n*.env\n!*.env.example\n!*.env.sample\n"
	return os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644)
}
