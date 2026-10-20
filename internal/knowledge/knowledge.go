// Package knowledge defines the schema for .stackfork/knowledge.json (design
// doc D10, task 13.1): what a `stackfork learn` scan and session discover
// about how a project runs in Docker, safe to commit because it never holds
// a secret value, only names and structure.
package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// CurrentSchema is the schema Save writes. Load also accepts schema 1 (the
// free-text recipe, converted in memory) and refuses anything higher.
const CurrentSchema = 2

// Status values (design.md D10).
const (
	StatusDraft    = "draft"
	StatusVerified = "verified"
	StatusStale    = "stale"
)

// Purpose values an EnvKey is grouped under (task 13.4).
const (
	PurposeDatabase = "database"
	PurposeAuth     = "auth"
	PurposeMail     = "mail"
	PurposeStorage  = "storage"
	PurposeRegistry = "registry"
	PurposeOther    = "other"
)

// Knowledge is the root of knowledge.json.
type Knowledge struct {
	Schema int    `json:"schema"`
	Status string `json:"status"`

	// Inputs are the files the scan read, with the content hash it read at
	// the time, so status/where can later tell knowledge is stale (task
	// 13.7 uses this; this task only defines and populates it).
	Inputs []Input `json:"inputs,omitempty"`

	Facts Facts `json:"facts"`

	// Refusals lists what `prepare`'s isolation step would refuse or
	// rewrite, collected by compose.IsolateDryRun (task 13.3) rather than
	// duplicated here.
	Refusals []compose.Finding `json:"refusals,omitempty"`

	// Risks are startup-time footguns flagged with file:line (task 13.4):
	// reset/seed on every start, writes to .env/.npmrc, hardcoded
	// `docker compose -p`/`docker exec`. Never modified, only reported.
	Risks []Risk `json:"risks,omitempty"`

	// EnvKeys are environment variable NAMES ONLY, grouped by inferred
	// purpose; no value from any env file is ever recorded (task 13.4).
	EnvKeys []EnvKey `json:"env_keys,omitempty"`

	// AdapterFiles are the project-specific Compose overrides and other
	// files the learn session wrote (task 13.8/13.9); empty on a fresh scan.
	AdapterFiles []string `json:"adapter_files,omitempty"`

	// Plan is the ordered startup sequence (schema 2).
	Plan Plan `json:"plan"`

	// Recipe is the transitional schema 1 view of Plan, kept so today's
	// readers compile; Load fills it from Plan on schema 2 and Save writes
	// it. Removed once those readers move to Plan.
	Recipe Recipe `json:"recipe"`

	// Answers and Gotchas accumulate across a (possibly resumed) learn
	// session (task 13.5/13.8); empty on a fresh scan.
	Answers []Answer `json:"answers,omitempty"`
	Gotchas []Gotcha `json:"gotchas,omitempty"`

	// Verification is set only once the user confirms the app works (task
	// 13.6); nil while Status is draft.
	Verification *Verification `json:"verification,omitempty"`
}

// Input is one file the scan read, with its content hash at scan time.
// Path is relative to the project root, never absolute: knowledge.json is
// meant to be committed (design.md D10), and an absolute path would both
// leak whoever ran `learn`'s home directory into a shared file and be
// simply wrong for every other teammate's own clone, at its own path.
type Input struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Facts is everything the deterministic scan (task 13.2) observed.
type Facts struct {
	ComposeFiles []string  `json:"compose_files,omitempty"`
	Services     []Service `json:"services,omitempty"`

	PackageManager string   `json:"package_manager,omitempty"` // bun, pnpm, yarn, npm
	Lockfile       string   `json:"lockfile,omitempty"`
	OtherManifests []string `json:"other_manifests,omitempty"` // go.mod, requirements.txt, pyproject.toml, Gemfile: detection only

	// RuntimeVersions maps a runtime name (node, go, ...) to the version
	// found in a version file (.node-version, .nvmrc, .tool-versions,
	// .bun-version, go.mod's `go` directive).
	RuntimeVersions map[string]string `json:"runtime_versions,omitempty"`

	Scripts []Script `json:"scripts,omitempty"`
}

// Service is one rendered Compose service's shape.
type Service struct {
	Name           string   `json:"name"`
	Image          string   `json:"image,omitempty"`
	Build          bool     `json:"build,omitempty"`
	DBEngine       string   `json:"db_engine,omitempty"` // postgres, mysql, mariadb, mongo, redis
	DBMajorVersion string   `json:"db_major_version,omitempty"`
	Ports          []string `json:"ports,omitempty"`
	Volumes        []string `json:"volumes,omitempty"`
	Networks       []string `json:"networks,omitempty"`
	Healthcheck    bool     `json:"healthcheck,omitempty"`
	Profiles       []string `json:"profiles,omitempty"`
}

// Script is one dev/migrate/seed/reset/test script found by name matching
// in package.json, a Makefile or a justfile.
type Script struct {
	Role    string `json:"role"` // dev, migrate, seed, reset, test
	Name    string `json:"name"`
	Source  string `json:"source"` // package.json, Makefile, justfile
	Command string `json:"command,omitempty"`
}

// Risk is a flagged startup-time footgun with its location (task 13.4).
type Risk struct {
	Kind   string `json:"kind"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Detail string `json:"detail"`
}

// EnvKey is one environment variable name, never its value.
type EnvKey struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"` // database, auth, mail, storage, registry, other
	Source  string `json:"source"`  // e.g. ".env.example", "compose:services.db.env_file"
}

// Recipe is the confirmed startup sequence (task 13.5/13.6).
type Recipe struct {
	Prepare  string `json:"prepare,omitempty"`
	Up       string `json:"up,omitempty"`
	Migrate  string `json:"migrate,omitempty"`
	SeedOnce string `json:"seed_once,omitempty"`
	// SeedService is the service SeedOnce runs in; empty = the primary app.
	SeedService string `json:"seed_service,omitempty"`
	// SeedPending marks a seed_once the scan proposed that nobody decided
	// on yet; "none" is the recorded answer "never seed".
	SeedPending bool     `json:"seed_pending,omitempty"`
	Login       []string `json:"login,omitempty"`
}

// Answer records a question the learn session had to ask the user, and
// their answer (task 13.8).
type Answer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Gotcha records a problem hit during the learn session and its fix (task
// 13.8).
type Gotcha struct {
	Problem string `json:"problem"`
	Fix     string `json:"fix"`
}

// Verification records that the user confirmed the app works (task 13.6).
type Verification struct {
	Date         string   `json:"date"`
	Checks       []string `json:"checks"`
	Confirmation string   `json:"confirmation"` // the user's own quoted words
}

// Load reads and decodes knowledge.json. Schema 1 converts its recipe to a
// plan in memory (the file is untouched until Save); a higher schema, or an
// invalid plan, is refused.
func Load(path string) (*Knowledge, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var k Knowledge
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	switch k.Schema {
	case 1:
		k.Plan = FromRecipe(k.Recipe)
	case CurrentSchema:
		// ToRecipe is lossy ("none" seed, free-text commands), so a recipe
		// Save already wrote beside the plan is kept as is.
		if reflect.DeepEqual(k.Recipe, Recipe{}) {
			k.Recipe = ToRecipe(k.Plan)
		}
	default:
		return nil, fmt.Errorf("%s: schema %d is not supported (this build understands schemas 1 and %d); upgrade stackfork or re-run learn", path, k.Schema, CurrentSchema)
	}
	if err := ValidatePlan(k.Plan); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &k, nil
}

// Save writes knowledge.json atomically (temp file + rename) as schema 2.
func (k *Knowledge) Save(path string) error {
	k.Schema = CurrentSchema
	// Transitional: readers that still edit Recipe leave Plan stale; a
	// non-zero Recipe that differs from the plan's view wins.
	if cur := ToRecipe(k.Plan); !reflect.DeepEqual(k.Recipe, cur) {
		if !reflect.DeepEqual(k.Recipe, Recipe{}) {
			k.Plan = FromRecipe(k.Recipe)
		} else {
			k.Recipe = cur
		}
	}
	if err := ValidatePlan(k.Plan); err != nil {
		return err
	}
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return atomicWrite(path, b, 0o644)
}

// atomicWrite mirrors state.WritePrivate's temp-file-then-rename pattern,
// but at 0644: unlike state's manifests, knowledge.json is meant to be
// committed and read by teammates (design.md D10), not kept private.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// SaveMarkdown writes knowledge.md, generated from k, atomically.
func (k *Knowledge) SaveMarkdown(path string) error {
	return atomicWrite(path, []byte(k.Markdown()), 0o644)
}

// Markdown renders a short human- and agent-readable summary of k (task
// 13.1). It never includes an env value: EnvKeys only ever holds names.
func (k *Knowledge) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Project knowledge\n\n")
	fmt.Fprintf(&b, "Status: **%s**\n\n", orDefault(k.Status, StatusDraft))

	if len(k.Facts.ComposeFiles) == 0 {
		fmt.Fprintf(&b, "No Compose files found.\n\n")
	} else {
		fmt.Fprintf(&b, "## Compose\n\nFiles: %s\n\n", strings.Join(k.Facts.ComposeFiles, ", "))
		if len(k.Facts.Services) > 0 {
			fmt.Fprintf(&b, "| service | image | db | ports | healthcheck |\n")
			fmt.Fprintf(&b, "|---|---|---|---|---|\n")
			for _, s := range k.Facts.Services {
				db := s.DBEngine
				if db != "" && s.DBMajorVersion != "" {
					db = db + " " + s.DBMajorVersion
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %v |\n",
					s.Name, orDefault(s.Image, "(build)"), orDefault(db, "-"),
					strings.Join(s.Ports, ", "), s.Healthcheck)
			}
			b.WriteString("\n")
		}
	}

	if k.Facts.PackageManager != "" || k.Facts.Lockfile != "" || len(k.Facts.OtherManifests) > 0 {
		fmt.Fprintf(&b, "## Package manager\n\n")
		if k.Facts.PackageManager != "" {
			fmt.Fprintf(&b, "- Manager: %s (%s)\n", k.Facts.PackageManager, k.Facts.Lockfile)
		}
		for _, m := range k.Facts.OtherManifests {
			fmt.Fprintf(&b, "- Also detected: %s\n", m)
		}
		b.WriteString("\n")
	}

	if len(k.Facts.RuntimeVersions) > 0 {
		fmt.Fprintf(&b, "## Runtime versions\n\n")
		for _, name := range sortedKeys(k.Facts.RuntimeVersions) {
			fmt.Fprintf(&b, "- %s: %s\n", name, k.Facts.RuntimeVersions[name])
		}
		b.WriteString("\n")
	}

	if len(k.Facts.Scripts) > 0 {
		fmt.Fprintf(&b, "## Scripts\n\n")
		for _, s := range k.Facts.Scripts {
			fmt.Fprintf(&b, "- **%s** (%s, %s): `%s`\n", s.Role, s.Name, s.Source, s.Command)
		}
		b.WriteString("\n")
	}

	if len(k.Refusals) > 0 {
		fmt.Fprintf(&b, "## What prepare would refuse or rewrite\n\n")
		for _, f := range k.Refusals {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Action, f.Target, f.Reason)
		}
		b.WriteString("\n")
	}

	if len(k.Risks) > 0 {
		fmt.Fprintf(&b, "## Risks\n\n")
		for _, r := range k.Risks {
			fmt.Fprintf(&b, "- %s:%d [%s] %s\n", r.File, r.Line, r.Kind, r.Detail)
		}
		b.WriteString("\n")
	}

	if len(k.EnvKeys) > 0 {
		fmt.Fprintf(&b, "## Environment variables (names only)\n\n")
		for _, purpose := range []string{PurposeDatabase, PurposeAuth, PurposeMail, PurposeStorage, PurposeRegistry, PurposeOther} {
			var names []string
			for _, e := range k.EnvKeys {
				if e.Purpose == purpose {
					names = append(names, e.Name)
				}
			}
			if len(names) == 0 {
				continue
			}
			sort.Strings(names)
			fmt.Fprintf(&b, "- %s: %s\n", purpose, strings.Join(names, ", "))
		}
		b.WriteString("\n")
	}

	if len(k.Plan.Steps) > 0 {
		fmt.Fprintf(&b, "## Startup plan\n\nStatus: %s\n\n", orDefault(k.Plan.Status, PlanProposed))
		for _, s := range k.Plan.Steps {
			fmt.Fprintf(&b, "- %s · %s · %s\n", s.ID, s.Kind, s.detail())
		}
		b.WriteString("\n")
	}

	if len(k.Gotchas) > 0 {
		fmt.Fprintf(&b, "## Gotchas\n\n")
		for _, g := range k.Gotchas {
			fmt.Fprintf(&b, "- %s -> %s\n", g.Problem, g.Fix)
		}
		b.WriteString("\n")
	}

	if len(k.Answers) > 0 {
		fmt.Fprintf(&b, "## Answers\n\n")
		for _, a := range k.Answers {
			fmt.Fprintf(&b, "- Q: %s\n  A: %s\n", a.Question, a.Answer)
		}
		b.WriteString("\n")
	}

	if k.Verification != nil {
		fmt.Fprintf(&b, "## Verified\n\n")
		fmt.Fprintf(&b, "- Date: %s\n- Checks: %s\n- Confirmation: %q\n\n",
			k.Verification.Date, strings.Join(k.Verification.Checks, ", "), k.Verification.Confirmation)
	}

	return b.String()
}

// StaleInputs returns the recorded input paths (relative to root, as
// stored) whose content no longer matches the hash captured at scan time
// (design.md D10: "verified -> stale when any recorded input hash
// changes"), or nil if every input is unchanged (or unreadable, which also
// counts as changed - a missing input is itself a reason to re-run learn).
// Callers only need this when Status is verified; a draft doesn't track
// staleness.
func (k *Knowledge) StaleInputs(root string) []string {
	var changed []string
	for _, in := range k.Inputs {
		h, err := state.SHA256File(filepath.Join(root, in.Path))
		if err != nil || h != in.SHA256 {
			changed = append(changed, in.Path)
		}
	}
	return changed
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
