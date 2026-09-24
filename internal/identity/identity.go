// Package identity ports the naming, hashing and hostname rules from
// scripts/compose.py's project_name/browser_hostname. Prefix stays "idv" for
// now (task 3.2/3.7): the D1 rename to "sf" is a later task.
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
	// Prefix is the instance-name prefix. Kept as the Python "idv" until 3.7.
	Prefix = "idv"
	// LabelRoot marks a service with its checkout root, so a second branch
	// stack from the same folder can be detected and refused.
	LabelRoot = "io.isolated-dev.root"
	// StateDirName is the directory under ~/.local/state holding instance state.
	StateDirName = "isolated-dev"
	// RenderProject is the throwaway Compose project name used only to render
	// (never started), matching the Python literal.
	RenderProject = "idv-render"
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

// ProjectName mirrors Python's project_name(root, branch).
func ProjectName(root, branch string) (string, error) {
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
	return fmt.Sprintf("%s-%s-%s", Prefix, slug, digest), nil
}

var hostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.localhost$`)

// BrowserHostname mirrors Python's browser_hostname(project, selected).
func BrowserHostname(project, selected string) (string, error) {
	hostname := selected
	if hostname == "" {
		hostname = strings.TrimPrefix(project, Prefix+"-") + ".localhost"
	}
	if !hostnamePattern.MatchString(hostname) {
		return "", errors.New("Hostname must be one DNS label followed by .localhost (for example eup-274.localhost)")
	}
	return hostname, nil
}

// StateDir returns the default state directory (~/.local/state/isolated-dev).
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", StateDirName), nil
}

// InstancePattern validates a --instance/--down argument: "idv-[a-z0-9-]+".
var InstancePattern = regexp.MustCompile(`^` + Prefix + `-[a-z0-9-]+$`)
