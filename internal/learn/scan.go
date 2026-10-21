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

// envFileDeclarations conservatively extracts every path a compose source
// file's `env_file:` list entries declare - stdlib only, no YAML parser,
// the same conservative line-scan approach as
// inventory.serviceUsesEnvFile/serviceBlockPattern, good enough to answer
// this one question. Used only to decide which missing files Scan should
// tolerate (see touchMissingEnvFiles), never to read a value.
func envFileDeclarations(text string) []string {
	lines := strings.Split(text, "\n")
	var out []string
	for i, line := range lines {
		if strings.TrimSpace(line) != "env_file:" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		for _, entry := range lines[i+1:] {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				continue
			}
			entryIndent := len(entry) - len(strings.TrimLeft(entry, " \t"))
			if entryIndent <= indent || !strings.HasPrefix(trimmed, "-") {
				break
			}
			path := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			path = strings.TrimSpace(strings.TrimPrefix(path, "path:"))
			path = strings.Trim(path, `"'`)
			if path != "" {
				out = append(out, path)
			}
		}
	}
	return out
}

// touchMissingEnvFiles creates an empty placeholder for every env_file a
// compose source file declares but that doesn't exist yet, and returns a
// cleanup func that removes exactly the ones it created. A project's real
// env_file (e.g. .env) is typically gitignored, so a fresh checkout
// commonly declares one that doesn't exist yet - that's a fact for the
// recipe/gotchas step, not a scan failure (see the comment at Scan's
// Render call), and `docker compose config --no-env-resolution` tolerates
// this on newer Compose versions by itself. It does not on every version
// (verified: hard-fails on v2.38.2, "env file ... not found", even with
// --no-env-resolution/--no-path-resolution/--no-consistency), so Scan
// creates the empty file itself rather than depending on that - its
// contents are never resolved either way (--no-env-resolution), so empty
// is exactly equivalent to "not resolved".
func touchMissingEnvFiles(root string, composeFiles []string) (cleanup func(), err error) {
	var created []string
	cleanup = func() {
		for _, p := range created {
			os.Remove(p) //nolint:errcheck // best-effort: a placeholder this func itself created
		}
	}
	for _, f := range composeFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			cleanup()
			return nil, err
		}
		dir := filepath.Dir(f)
		for _, ref := range envFileDeclarations(string(data)) {
			path := ref
			if !filepath.IsAbs(path) {
				path = filepath.Join(dir, path)
			}
			if !underRoot(path, root) {
				continue // never write outside the checkout
			}
			if fileExists(path) {
				continue
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				cleanup()
				return nil, err
			}
			created = append(created, path)
		}
	}
	return cleanup, nil
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
	var buildContexts []string
	if len(composeFiles) > 0 {
		abs := make([]string, len(composeFiles))
		for i, f := range composeFiles {
			abs[i] = filepath.Join(root, f)
		}
		cleanup, err := touchMissingEnvFiles(root, abs)
		if err != nil {
			return nil, err
		}
		defer cleanup()

		rendered, _, err := compose.Render(root, abs, "", "", nil)
		if err != nil {
			return nil, err
		}
		k.Facts.Services = servicesFromRendered(rendered)
		buildContexts = buildContextsUnderRoot(rendered, root)
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

	k.Facts.MigrationDirs = detectMigrationDirs(root, buildContexts)

	k.Facts.Scripts = detectScripts(root)
	for _, name := range []string{"Makefile", "justfile"} {
		if fileExists(filepath.Join(root, name)) {
			inputs[filepath.Join(root, name)] = true
		}
	}

	k.Plan = ProposePlan(root, k)
	k.Entry = ProposeEntry(k)

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
