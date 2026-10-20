// Package live holds the pure parts of live mode: the watch set, the
// poller, the debounce and the baseline store (add-live-mode D4, D5, D7).
package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Categories decide the action of a change (design D5, D6).
const (
	CatBuild      = "build"
	CatCompose    = "compose"
	CatMigrations = "migrations"
	CatPlan       = "plan"
)

// MaxFiles caps the files watched for one checkout (design D4).
const MaxFiles = 2000

// Override is one `live.watch` entry of knowledge.json. Group 1 (the
// knowledge `live` settings) lives on another branch, so the caller maps
// knowledge.Live into these plain structs.
type Override struct {
	Path    string // relative to the checkout, "/" separated
	Action  string // rebuild, prepare or migrate
	Service string // for rebuild
}

// Overrides is the knowledge `live` object.
type Overrides struct {
	Watch  []Override
	Ignore []string
}

// Inputs is everything the watch set derives from. The caller reads them
// from the instance (compose.json, manifest) and from knowledge.json.
type Inputs struct {
	Checkout       string
	ComposeJSON    []byte   // the instance's compose.json
	PackageManager string   // facts.package_manager
	OtherManifests []string // facts.other_manifests
	ComposeFiles   []string // manifest files (absolute, or relative to the checkout)
	EnvFile        string   // manifest env_file, "" when none
	AdapterFiles   []string // knowledge adapter_files (relative)
	MigrationDirs  []string // facts.migration_dirs (relative)
	Overrides      Overrides
}

// Entry is one watched path: a file, or a directory watched to depth 2.
type Entry struct {
	Path     string `json:"-"`    // absolute
	Rel      string `json:"path"` // relative to the checkout, "/" separated
	Category string `json:"category"`
	Service  string `json:"service,omitempty"`
}

var jsLocks = map[string][]string{
	"bun":  {"bun.lock", "bun.lockb"},
	"pnpm": {"pnpm-lock.yaml"},
	"yarn": {"yarn.lock"},
	"npm":  {"package-lock.json", "npm-shrinkwrap.json"},
}

// others maps a manifest to its lockfiles; with a lockfile present the
// manifest is not watched (design D5).
var others = map[string][]string{
	"go.mod":           {"go.sum"},
	"requirements.txt": nil,
	"pyproject.toml":   {"poetry.lock", "uv.lock"},
	"Gemfile":          {"Gemfile.lock"},
	"composer.json":    {"composer.lock"},
	"Cargo.toml":       {"Cargo.lock"},
}

// WatchSet derives the watched paths and their categories. It refuses a
// knowledge-supplied path that resolves outside the checkout, an override
// `service` the instance does not have, and a set above MaxFiles.
// Paths the Compose model or the manifest name outside the checkout are
// not ours to watch and are skipped.
func WatchSet(in Inputs) ([]Entry, error) {
	root, err := filepath.EvalSymlinks(in.Checkout)
	if err != nil {
		return nil, err
	}
	var model struct {
		Services map[string]struct {
			Build json.RawMessage `json:"build"`
		} `json:"services"`
	}
	if len(in.ComposeJSON) > 0 {
		if err := json.Unmarshal(in.ComposeJSON, &model); err != nil {
			return nil, fmt.Errorf("compose.json: %w", err)
		}
	}
	w := &builder{checkout: in.Checkout, root: root, seen: map[string]bool{}}
	ignored := map[string]bool{}
	for _, p := range in.Overrides.Ignore {
		ignored[cleanRel(p)] = true
	}

	names := make([]string, 0, len(model.Services))
	for n := range model.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ctx, df, ok := buildOf(model.Services[name].Build, in.Checkout)
		if !ok {
			continue // no build: nothing to rebuild
		}
		add := func(abs string) { w.add(abs, CatBuild, name, ignored, false) }
		add(filepath.Join(ctx, df))
		add(filepath.Join(ctx, ".dockerignore"))
		for _, f := range depFiles(ctx, in.PackageManager, in.OtherManifests) {
			add(f)
		}
	}
	for _, f := range in.ComposeFiles {
		w.add(abs(in.Checkout, f), CatCompose, "", ignored, false)
	}
	if in.EnvFile != "" {
		w.add(abs(in.Checkout, in.EnvFile), CatCompose, "", ignored, false)
	}
	for _, f := range in.AdapterFiles {
		w.add(abs(in.Checkout, f), CatCompose, "", ignored, true)
	}
	for _, d := range in.MigrationDirs {
		w.add(abs(in.Checkout, d), CatMigrations, "", ignored, true)
	}
	w.add(filepath.Join(in.Checkout, ".stackfork", "knowledge.json"), CatPlan, "", ignored, false)

	for _, o := range in.Overrides.Watch {
		cat := map[string]string{"rebuild": CatBuild, "prepare": CatCompose, "migrate": CatMigrations}[o.Action]
		if cat == "" {
			return nil, fmt.Errorf("live.watch %s: unknown action %q", o.Path, o.Action)
		}
		if cat == CatBuild {
			if _, ok := model.Services[o.Service]; !ok {
				return nil, fmt.Errorf("live.watch %s: service %q is not a service of this instance", o.Path, o.Service)
			}
		}
		w.add(abs(in.Checkout, o.Path), cat, o.Service, nil, true)
	}
	if w.err != nil {
		return nil, w.err
	}
	return w.entries, checkCap(w.entries)
}

type builder struct {
	checkout, root string
	entries        []Entry
	seen           map[string]bool
	err            error
}

// add records abs unless ignored or already present. strict makes an
// outside-the-checkout path an error instead of a skip.
func (b *builder) add(abs, cat, svc string, ignored map[string]bool, strict bool) {
	rel, err := filepath.Rel(b.checkout, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if strict && b.err == nil {
			b.err = fmt.Errorf("%s is outside the checkout", abs)
		}
		return
	}
	rel = filepath.ToSlash(rel)
	if !b.inside(abs) {
		if strict && b.err == nil {
			b.err = fmt.Errorf("%s resolves outside the checkout", rel)
		}
		return
	}
	if ignored[rel] {
		return
	}
	key := cat + "\x00" + svc + "\x00" + rel
	if b.seen[key] {
		return
	}
	b.seen[key] = true
	b.entries = append(b.entries, Entry{Path: abs, Rel: rel, Category: cat, Service: svc})
}

// inside reports whether abs, after symbolic links, stays in the checkout.
// A path that does not exist yet is judged by its nearest existing parent.
func (b *builder) inside(abs string) bool {
	p := abs
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			rel, err := filepath.Rel(b.root, r)
			return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

func abs(checkout, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(checkout, filepath.FromSlash(p))
}

func cleanRel(p string) string { return filepath.ToSlash(filepath.Clean(filepath.FromSlash(p))) }

// buildOf reads a rendered service's `build` (a context string, or a map
// with context and dockerfile). ok is false for a service with no build.
func buildOf(raw json.RawMessage, checkout string) (ctx, dockerfile string, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return abs(checkout, s), "Dockerfile", s != ""
	}
	var m struct{ Context, Dockerfile string }
	if json.Unmarshal(raw, &m) != nil {
		return "", "", false
	}
	if m.Context == "" {
		m.Context = "."
	}
	if m.Dockerfile == "" {
		m.Dockerfile = "Dockerfile"
	}
	ctx = abs(checkout, m.Context)
	if filepath.IsAbs(m.Dockerfile) {
		// Compose can render the Dockerfile absolute; keep it relative to
		// the context so the join below lands on it.
		if r, err := filepath.Rel(ctx, m.Dockerfile); err == nil {
			m.Dockerfile = r
		}
	}
	return ctx, m.Dockerfile, true
}

// depFiles is the lockfile of each package manager in dir, else its manifest.
func depFiles(dir, pm string, manifests []string) []string {
	exists := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	pick := func(manifest string, locks []string) []string {
		for _, l := range locks {
			if exists(l) {
				return []string{filepath.Join(dir, l)}
			}
		}
		if exists(manifest) {
			return []string{filepath.Join(dir, manifest)}
		}
		return nil
	}
	var out []string
	if locks, ok := jsLocks[pm]; ok {
		out = append(out, pick("package.json", locks)...)
	}
	for _, m := range manifests {
		if locks, ok := others[m]; ok {
			out = append(out, pick(m, locks)...)
		}
	}
	return out
}

// checkCap refuses a set above MaxFiles and names the largest entry.
func checkCap(entries []Entry) error {
	total, big, bigN := 0, "", 0
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		n := len(ListFiles(e.Path))
		total += n
		if n > bigN {
			big, bigN = e.Rel, n
		}
	}
	if total > MaxFiles {
		return fmt.Errorf("watch set has %d files (limit %d); largest entry: %s (%d files)", total, MaxFiles, big, bigN)
	}
	return nil
}
