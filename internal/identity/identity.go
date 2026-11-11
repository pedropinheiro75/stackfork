// Package identity ports the naming, hashing and hostname rules from the
// original Python CLI's project_name/browser_hostname, then (task 3.7)
// switches the public identity to the "stackfork"/"sf" constants from
// design.md D1. The legacy "idv" constants are kept so task 3.8 can still
// read state and containers created by the previous identity; nothing new
// is ever written under them.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	// Prefix is the instance-name prefix for newly prepared instances.
	Prefix = "sf"
	// LabelNS is the Docker label namespace for newly prepared instances.
	LabelNS = "io.stackfork"
	// LabelRoot marks a service with its checkout root, so a second branch
	// stack from the same folder can be detected and refused.
	LabelRoot = LabelNS + ".root"
	// LabelHTTP and LabelHTTPS mark a service's target ports as serving
	// HTTP/HTTPS (task 7.1, design.md D7): a plain Docker label, not the
	// x-stackfork Compose extension the design first proposed, because
	// `docker compose config --format json` drops service-level x-* keys
	// on the Compose version this was verified against (a label survives).
	// Value is a comma-separated list of target ports, e.g. "5173,3000".
	LabelHTTP  = LabelNS + ".http"
	LabelHTTPS = LabelNS + ".https"
	// LabelPrimary marks the one service the hostname proxy routes an
	// instance's hostname to, when a service declares more than one HTTP/HTTPS
	// port (task 9.2, design.md D7 layer 3): "io.stackfork.primary=true".
	// Without it, the proxy falls back to the first declared/inferred HTTP
	// service (compose.PrimaryHTTPService).
	LabelPrimary = LabelNS + ".primary"
	// LabelProxy marks the tool-owned Caddy container itself (task 9.1),
	// never a user service, so `gc` and other instance scans can skip it.
	LabelProxy = LabelNS + ".proxy"
	// StateDirName is the directory under ~/.local/state holding instance state.
	StateDirName = "stackfork"
	// RenderProject is the throwaway Compose project name used only to render
	// (never started).
	RenderProject = "sf-render"
	// EntrypointName is the name the template Dockerfile copies
	// .agent-dev/entrypoint.sh to inside the image.
	EntrypointName = "stackfork-dev-entrypoint"

	// LegacyPrefix, LegacyLabelRoot and LegacyStateDirName are the previous
	// "isolated-dev" identity (design.md D2). They are read-only: no new
	// instance is ever written under them, but existing state and running
	// containers created by the previous version stay manageable through
	// task 3.8's compatibility reads.
	LegacyPrefix       = "idv"
	LegacyLabelRoot    = "io.isolated-dev.root"
	LegacyStateDirName = "isolated-dev"
)

var slugInvalid = regexp.MustCompile(`[^a-z0-9-]`)

// ResolvePath mirrors Python's Path.resolve(): an absolute path with
// symlinks resolved as far as possible. Unlike filepath.EvalSymlinks it does
// not fail when the path (or a trailing component) does not exist.
func ResolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return stripExtendedPrefix(trueCase(resolved)), nil
	}
	return stripExtendedPrefix(abs), nil
}

// stripExtendedPrefix turns a Windows extended-length path (\\?\C:\x,
// \\?\UNC\srv\share) into its plain form, so one checkout never hashes to
// two project names. EvalSymlinks can return the extended form on Windows.
func stripExtendedPrefix(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	return trimExtended(p)
}

func trimExtended(p string) string {
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	if rest, ok := strings.CutPrefix(p, `\\?\`); ok {
		return rest
	}
	return p
}

// projectName is the shared implementation behind ProjectName and
// LegacyProjectName: it mirrors Python's project_name(root, branch), keyed
// by an instance-name prefix.
func projectName(prefix, root, branch string) (string, error) {
	resolved, err := ResolvePath(root)
	if err != nil {
		return "", err
	}
	slug := slugName(filepath.Base(resolved))
	digest := projectDigest(resolved, branch)
	return fmt.Sprintf("%s-%s-%s", prefix, slug, digest), nil
}

// slugName lower-cases a folder name for use in an instance name or address:
// other characters become "-", at most 24 characters, never empty.
func slugName(base string) string {
	slug := slugInvalid.ReplaceAllString(strings.ToLower(base), "-")
	if len(slug) > 24 {
		slug = slug[:24]
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "app"
	}
	return slug
}

// projectDigest is the 12-hex digest in the project name.
func projectDigest(resolved, branch string) string {
	sum := sha256.Sum256([]byte(resolved + "\x00" + branch))
	return hex.EncodeToString(sum[:])[:12]
}

// ProjectName mirrors Python's project_name(root, branch), using the
// current public prefix.
func ProjectName(root, branch string) (string, error) {
	return projectName(Prefix, root, branch)
}

// LegacyProjectName computes the project name an existing idv- instance for
// this checkout+branch would have (task 3.8's legacy resolution).
func LegacyProjectName(root, branch string) (string, error) {
	return projectName(LegacyPrefix, root, branch)
}

var hostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.localhost$`)

// BrowserHostname mirrors Python's browser_hostname(project, selected),
// stripping whichever instance prefix (new or legacy) the project carries.
func BrowserHostname(project, selected string) (string, error) {
	hostname := selected
	if hostname == "" {
		base := project
		switch {
		case strings.HasPrefix(project, Prefix+"-"):
			base = strings.TrimPrefix(project, Prefix+"-")
		case strings.HasPrefix(project, LegacyPrefix+"-"):
			base = strings.TrimPrefix(project, LegacyPrefix+"-")
		}
		hostname = base + ".localhost"
	}
	if !hostnamePattern.MatchString(hostname) {
		return "", errors.New("Hostname must be one DNS label followed by .localhost (for example eup-274.localhost)")
	}
	return hostname, nil
}

// DefaultHostnameTemplate is the address pattern when config.json has none.
const DefaultHostnameTemplate = "{app}-{worktree}"

// ReservedHostname is the dashboard's own address.
const ReservedHostname = "stackfork.localhost"

var (
	hostnameTokens = []string{"{app}", "{worktree}", "{hash}"}
	multiDash      = regexp.MustCompile(`-{2,}`)
)

// ValidateHostnameTemplate accepts "" (the default) or a template whose
// tokens are {app}, {worktree} and {hash} and whose other text is a-z, 0-9
// and "-".
func ValidateHostnameTemplate(t string) error {
	rest := t
	for _, tok := range hostnameTokens {
		rest = strings.ReplaceAll(rest, tok, "")
	}
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return fmt.Errorf("Invalid address pattern %q: use the tokens {app}, {worktree} and {hash}, plus only a-z, 0-9 and - (for example {app}-{worktree})", t)
		}
	}
	return nil
}

// RenderHostname fills a template. app and worktree are folder names; a
// checkout that is its own main one has the same name for both, so its
// {worktree} renders empty next to {app} (acme-web, not acme-web-acme-web).
// When the result is empty or not a valid label, it falls back to the old
// "<worktree slug>-<hash>" form.
func RenderHostname(tmpl, app, worktree, hash string) string {
	if tmpl == "" {
		tmpl = DefaultHostnameTemplate
	}
	// Full folder names first; the 24-character slugs only when the label
	// would pass 63 (they keep {app}-{worktree}-{hash} within the limit).
	for _, slug := range []func(string) string{fullSlug, slugName} {
		if label := renderLabel(tmpl, slug(app), slug(worktree), hash); hostnamePattern.MatchString(label + ".localhost") {
			return label + ".localhost"
		}
	}
	return slugName(worktree) + "-" + hash + ".localhost"
}

func renderLabel(tmpl, a, w, hash string) string {
	if a == w && strings.Contains(tmpl, "{app}") {
		w = ""
	}
	label := strings.NewReplacer("{app}", a, "{worktree}", w, "{hash}", hash).Replace(tmpl)
	return strings.Trim(multiDash.ReplaceAllString(label, "-"), "-")
}

// fullSlug is slugName without the 24-character limit.
func fullSlug(base string) string {
	slug := strings.Trim(slugInvalid.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if slug == "" {
		slug = "app"
	}
	return slug
}

// TemplateHostname renders the address for a new instance of root+branch, and
// the digest it uses. {app} is the main checkout's folder name.
func TemplateHostname(tmpl, root, branch string) (name, hash string, err error) {
	resolved, err := ResolvePath(root)
	if err != nil {
		return "", "", err
	}
	hash = projectDigest(resolved, branch)
	app := filepath.Base(resolved)
	if main := MainRepoOf(resolved); main != "" {
		if m, err := ResolvePath(main); err == nil {
			app = filepath.Base(m)
		}
	}
	return RenderHostname(tmpl, app, filepath.Base(resolved), hash), hash, nil
}

// UniqueHostname appends "-" and the first 6 hex of hash when name is the
// dashboard's or taken(name) says another instance holds it, and reports
// whether it did. A suffixed label over 63 characters falls back to the old
// "<slug>-<hash>" form of its first 24 characters.
func UniqueHostname(name, hash string, taken func(string) bool) (string, bool) {
	if name != ReservedHostname && !taken(name) {
		return name, false
	}
	label := strings.TrimSuffix(name, ".localhost") + "-" + hash[:6]
	if len(label) > 63 {
		label = slugName(label[:24]) + "-" + hash
	}
	return label + ".localhost", true
}

// stateDir joins the given state-dir name onto ~/.local/state, or onto
// $STACKFORK_STATE_HOME when set: tests set it so nothing they prepare ever
// lands in the developer's real state.
func stateDir(name string) (string, error) {
	if base := os.Getenv("STACKFORK_STATE_HOME"); base != "" {
		return filepath.Join(base, name), nil
	}
	if runtime.GOOS == "windows" {
		// Machine-local state: %LOCALAPPDATA%, not the roaming %APPDATA%.
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, name), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", name), nil
}

// StateDir returns the default state directory (~/.local/state/stackfork).
func StateDir() (string, error) {
	return stateDir(StateDirName)
}

// ConfigDir returns the config directory (~/.config/stackfork), or
// $STACKFORK_CONFIG_DIR when set. The override exists so tests don't touch
// the real ~/.config/stackfork/profiles/ (task 5.2).
func ConfigDir() (string, error) {
	if dir := os.Getenv("STACKFORK_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	if runtime.GOOS == "windows" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "stackfork"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "stackfork"), nil
}

// LegacyStateDir returns the previous version's default state directory
// (~/.local/state/isolated-dev), read-only.
func LegacyStateDir() (string, error) {
	return stateDir(LegacyStateDirName)
}

// InstancePattern validates a --instance/--down argument: it accepts both
// the current prefix ("sf-...") and the legacy one ("idv-...").
var InstancePattern = regexp.MustCompile(`^(?:` + Prefix + `|` + LegacyPrefix + `)-[a-z0-9-]+$`)

// IsLegacyInstance reports whether an instance/project name uses the legacy
// "idv-" prefix rather than the current one.
func IsLegacyInstance(name string) bool {
	return strings.HasPrefix(name, LegacyPrefix+"-")
}
