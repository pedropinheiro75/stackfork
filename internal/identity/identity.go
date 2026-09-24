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
		return resolved, nil
	}
	return abs, nil
}

// projectName is the shared implementation behind ProjectName and
// LegacyProjectName: it mirrors Python's project_name(root, branch), keyed
// by an instance-name prefix.
func projectName(prefix, root, branch string) (string, error) {
	resolved, err := ResolvePath(root)
	if err != nil {
		return "", err
	}
	base := filepath.Base(resolved)
	slug := slugInvalid.ReplaceAllString(strings.ToLower(base), "-")
	if len(slug) > 24 {
		slug = slug[:24]
	}
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "app"
	}
	sum := sha256.Sum256([]byte(resolved + "\x00" + branch))
	digest := hex.EncodeToString(sum[:])[:12]
	return fmt.Sprintf("%s-%s-%s", prefix, slug, digest), nil
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

// stateDir joins the given state-dir name onto ~/.local/state.
func stateDir(name string) (string, error) {
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
