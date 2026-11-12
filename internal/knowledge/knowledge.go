// Package knowledge defines the schema for .stackfork/knowledge.json (design
// doc D10, task 13.1): what a `stackfork learn` scan and session discover
// about how a project runs in Docker, safe to commit because it never holds
// a secret value, only names and structure.
package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/ecosystem"
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

	// Answers and Gotchas accumulate across a (possibly resumed) learn
	// session (task 13.5/13.8); empty on a fresh scan.
	Answers []Answer `json:"answers,omitempty"`
	Gotchas []Gotcha `json:"gotchas,omitempty"`

	// Secrets are the secret NAMES the project needs, never references or
	// values (add-secret-vault D6).
	Secrets []SecretDecl `json:"secrets,omitempty"`

	// Live holds the live-mode watch overrides (live-mode D5); not part of
	// the trust hash.
	Live Live `json:"live,omitzero"`

	// Entry and Links record where the app and its other useful pages are
	// (entry-links D1); not part of the trust hash (D3).
	Entry *Entry `json:"entry,omitempty"`
	Links []Link `json:"links,omitempty"`

	// Reload is how code changes reach each app; not part of the trust hash.
	Reload []Reload `json:"reload,omitempty"`

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

	// MigrationDirs are the existing migration directories, relative to the
	// root, found by the scan or recorded by hand (live-mode D11).
	MigrationDirs []string `json:"migration_dirs,omitempty"`
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
	// StartupMigrate and StartupSeed say the service's own start (Compose
	// entrypoint/command, the Dockerfile ENTRYPOINT/CMD, or a script they
	// run) already migrates or seeds, with where.
	StartupMigrate *StartupRun `json:"startup_migrate,omitempty"`
	StartupSeed    *StartupRun `json:"startup_seed,omitempty"`
}

// StartupRun is one migrate or seed command found in a service's start.
type StartupRun struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Cmd  string `json:"cmd"`
}

// Where is "file:line" (just the file when the line is unknown).
func (r StartupRun) Where() string {
	if r.Line > 0 {
		return fmt.Sprintf("%s:%d", r.File, r.Line)
	}
	return r.File
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
	RecipeHash   string   `json:"recipe_hash,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	RunID        int      `json:"run_id,omitempty"`
	Date         string   `json:"date"`
	Checks       []string `json:"checks"`
	Confirmation string   `json:"confirmation"` // the user's own quoted words
	// Entry is the target `learn verify` probed ("web:3000/app", "host-route
	// web:3000", or "none"); Links is how each recorded link answered.
	Entry string      `json:"entry,omitempty"`
	Links []LinkCheck `json:"links,omitempty"`
}

// LinkCheck is the probe result of one link at verify time.
type LinkCheck struct {
	Name     string `json:"name"`
	Answered bool   `json:"answered"`
	Status   int    `json:"status"`
}

// Load reads and decodes knowledge.json. Schema 1 converts its recipe to a
// plan in memory (the file is untouched until Save); a higher schema, or an
// invalid plan, is refused.
func Load(path string) (*Knowledge, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Schema 1 carried a free-text recipe; it is read only to convert.
	var raw struct {
		Knowledge
		Legacy Recipe `json:"recipe"`
		// RawSecrets shadows Knowledge.Secrets so the list is decoded strictly.
		RawSecrets json.RawMessage `json:"secrets"`
		// RawLive shadows Knowledge.Live for the same reason.
		RawLive json.RawMessage `json:"live"`
		// RawEntry and RawLinks shadow Entry and Links likewise.
		RawEntry json.RawMessage `json:"entry"`
		RawLinks json.RawMessage `json:"links"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	k := raw.Knowledge
	secrets, err := parseSecrets(raw.RawSecrets)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	k.Secrets = secrets
	if k.Live, err = parseLive(raw.RawLive); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if k.Entry, err = parseEntry(raw.RawEntry); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if k.Links, err = parseLinks(raw.RawLinks); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateEntryLinks(k.Entry, k.Links); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateReload(k.Reload); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	switch k.Schema {
	case 1:
		k.Plan = FromRecipe(raw.Legacy)
	case CurrentSchema:
	default:
		return nil, fmt.Errorf("%s: schema %d is not supported (this build understands schemas 1 and %d); upgrade stackfork or re-run learn", path, k.Schema, CurrentSchema)
	}
	if err := ValidatePlan(k.Plan); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateAdapterFiles(k.AdapterFiles); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if k.Status == StatusVerified && !k.VerificationCurrent() {
		k.Status = StatusDraft
	}
	return &k, nil
}

// LoadFirst loads the first knowledge.json found in dirs. A missing file
// moves on to the next dir; any other failure (broken JSON, unsupported
// schema, invalid plan) is returned, never silently turned into "nothing
// recorded". No file anywhere: nil, nil.
func LoadFirst(dirs ...string) (*Knowledge, error) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		k, err := Load(filepath.Join(d, ".stackfork", "knowledge.json"))
		if err == nil {
			return k, nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return nil, err
		}
	}
	return nil, nil
}

// Save writes knowledge.json atomically (temp file + rename) as schema 2.
func (k *Knowledge) Save(path string) error {
	k.Schema = CurrentSchema
	if err := ValidatePlan(k.Plan); err != nil {
		return err
	}
	if err := ValidateSecrets(k.Secrets); err != nil {
		return err
	}
	if err := ValidateAdapterFiles(k.AdapterFiles); err != nil {
		return err
	}
	if err := ValidateLive(k.Live); err != nil {
		return err
	}
	if err := ValidateEntryLinks(k.Entry, k.Links); err != nil {
		return err
	}
	if err := ValidateReload(k.Reload); err != nil {
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

// StackforkGitignore is the content of .stackfork/.gitignore as `learn` writes
// it. Knowledge and adapters are meant to be committed (design.md D10); only
// files that can hold a real value are excluded. Branch cleanup recognizes the
// file by this exact content.
const StackforkGitignore = "runtime.env\n*.env\n!*.env.example\n!*.env.sample\n"

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
			for _, s := range k.Facts.Services {
				if s.StartupMigrate != nil {
					fmt.Fprintf(&b, "- %s already migrates on every start (%s): %s\n", s.Name, s.StartupMigrate.Where(), s.StartupMigrate.Cmd)
				}
				if s.StartupSeed != nil {
					fmt.Fprintf(&b, "- %s already seeds on every start (%s): %s\n", s.Name, s.StartupSeed.Where(), s.StartupSeed.Cmd)
				}
			}
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

	k.entrySection(&b)

	if len(k.Reload) > 0 {
		fmt.Fprintf(&b, "## How code changes reach the app\n\n")
		for _, r := range k.Reload {
			line := fmt.Sprintf("- %s: %s", r.Service, r.Mode)
			if r.Proposed {
				line += " (proposed)"
			}
			if r.Evidence != "" {
				line += " [" + r.Evidence + "]"
			}
			if r.Note != "" {
				line += " " + r.Note
			}
			fmt.Fprintf(&b, "%s\n", line)
		}
		b.WriteString("\n")
	}

	if len(k.Secrets) > 0 {
		fmt.Fprintf(&b, "## Secrets (names only)\n\n")
		for _, sd := range k.Secrets {
			if sd.Hint != "" {
				fmt.Fprintf(&b, "- %s: %s\n", sd.Name, sd.Hint)
			} else {
				fmt.Fprintf(&b, "- %s\n", sd.Name)
			}
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
		if k.Status == StatusVerified && k.VerificationCurrent() {
			fmt.Fprintf(&b, "## Verified\n\n")
		} else {
			fmt.Fprintf(&b, "## Verification pending\n\n")
		}
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
		h, err := ecosystem.InputHash(filepath.Join(root, in.Path))
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

// ValidateAdapterFiles accepts only clean relative paths inside the
// repository. The entries become `prepare -f` values, and splitArgs lifts
// --npmrc=, --secret= and --state-dir= out of argv wherever they sit, so an
// entry that starts with '-' would be a flag the preflight never sees.
func ValidateAdapterFiles(files []string) error {
	for i, f := range files {
		switch {
		case f == "":
			return fmt.Errorf("adapter_files[%d] is empty", i)
		case strings.HasPrefix(f, "-"):
			return fmt.Errorf("adapter_files[%d] starts with '-': it would be read as a flag", i)
		case strings.IndexFunc(f, unicode.IsControl) >= 0:
			return fmt.Errorf("adapter_files[%d] has a control character", i)
		case strings.Contains(f, "\\") || !filepath.IsLocal(f) || path.Clean(f) != f:
			return fmt.Errorf("adapter_files[%d] must be a clean relative path inside the repository (forward slashes, no .. or ./)", i)
		}
	}
	return nil
}

// AddMigrationDir records a migration directory (no duplicates, sorted).
func (k *Knowledge) AddMigrationDir(p string) {
	for _, d := range k.Facts.MigrationDirs {
		if d == p {
			return
		}
	}
	k.Facts.MigrationDirs = append(k.Facts.MigrationDirs, p)
	sort.Strings(k.Facts.MigrationDirs)
}

// RemoveMigrationDir drops a recorded directory; it reports whether one was there.
func (k *Knowledge) RemoveMigrationDir(p string) bool {
	for i, d := range k.Facts.MigrationDirs {
		if d == p {
			k.Facts.MigrationDirs = append(k.Facts.MigrationDirs[:i:i], k.Facts.MigrationDirs[i+1:]...)
			return true
		}
	}
	return false
}
