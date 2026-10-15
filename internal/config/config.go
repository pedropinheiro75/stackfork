// Package config reads and writes stackfork's user config file
// (<config dir>/config.json): the folders the repo browser scans.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// DefaultMaxDepth is how deep below a root the scan looks for repos.
const DefaultMaxDepth = 6

// Config is the typed part of config.json. Keys this version does not know
// are kept in extra and written back untouched.
type Config struct {
	Roots    []string `json:"roots"`
	Exclude  []string `json:"exclude"`
	MaxDepth int      `json:"maxDepth"`

	extra map[string]json.RawMessage
}

// Path is the config file location (STACKFORK_CONFIG_DIR respected).
func Path() (string, error) {
	dir, err := identity.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config; a missing file is an empty config.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	c := &Config{extra: map[string]json.RawMessage{}}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := json.Unmarshal(data, &c.extra); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	for _, k := range []string{"roots", "exclude", "maxDepth"} {
		delete(c.extra, k)
	}
	return c, nil
}

// Save writes the config atomically (0600), keeping unknown keys.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	out := map[string]interface{}{}
	for k, v := range c.extra {
		out[k] = v
	}
	out["roots"] = nonNil(c.Roots)
	out["exclude"] = nonNil(c.Exclude)
	if c.MaxDepth != 0 {
		out["maxDepth"] = c.MaxDepth
	}
	return state.WritePrivate(p, out)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Depth is MaxDepth with the default applied.
func (c *Config) Depth() int {
	if c.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return c.MaxDepth
}

// ExpandHome turns a leading ~ into the home directory.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

// ResolvedRoots returns each root as an absolute, symlink-resolved path;
// roots that no longer exist are skipped.
func (c *Config) ResolvedRoots() []string {
	var out []string
	for _, r := range c.Roots {
		if p, err := resolve(r); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func resolve(p string) (string, error) {
	p, err := ExpandHome(strings.TrimSpace(p))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path (use /path or ~/path)", p)
	}
	return identity.ResolvePath(p)
}

// ValidateRoot checks a candidate root and returns its resolved path. It
// refuses a missing path, a file, "/", the bare home directory, a folder
// inside an existing root, and a folder that contains one.
func (c *Config) ValidateRoot(raw string) (string, error) {
	p, err := resolve(raw)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s does not exist", p)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a folder", p)
	}
	if p == "/" || p == filepath.VolumeName(p)+string(filepath.Separator) {
		return "", errors.New("refusing to scan the filesystem root")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if h, err := identity.ResolvePath(home); err == nil && h == p {
			return "", errors.New("refusing to scan the whole home directory; add a folder inside it")
		}
	}
	for _, r := range c.ResolvedRoots() {
		if r == p {
			return "", fmt.Errorf("%s is already a root", p)
		}
		if Inside(r, p) {
			return "", fmt.Errorf("%s is already inside the root %s", p, r)
		}
		if Inside(p, r) {
			return "", fmt.Errorf("%s contains the existing root %s; remove that first", p, r)
		}
	}
	return p, nil
}

// Inside reports whether child is strictly inside dir (both resolved).
func Inside(dir, child string) bool {
	rel, err := filepath.Rel(dir, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// AddRoot validates and appends a root.
func (c *Config) AddRoot(raw string) (string, error) {
	p, err := c.ValidateRoot(raw)
	if err != nil {
		return "", err
	}
	c.Roots = append(c.Roots, p)
	return p, nil
}

// RemoveRoot drops a root (matched as written or by resolved path). Nothing
// on disk is touched.
func (c *Config) RemoveRoot(raw string) error {
	want, _ := resolve(raw)
	for i, r := range c.Roots {
		if r == raw || r == want || (want != "" && func() bool { x, err := resolve(r); return err == nil && x == want }()) {
			c.Roots = append(c.Roots[:i:i], c.Roots[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%s is not a configured root", raw)
}

// AddExclude adds a path segment to skip; it must be a plain name.
func (c *Config) AddExclude(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("exclude %q must be a single folder name", name)
	}
	for _, e := range c.Exclude {
		if e == name {
			return nil
		}
	}
	c.Exclude = append(c.Exclude, name)
	sort.Strings(c.Exclude)
	return nil
}

// RemoveExclude drops an exclude name.
func (c *Config) RemoveExclude(name string) error {
	for i, e := range c.Exclude {
		if e == name {
			c.Exclude = append(c.Exclude[:i:i], c.Exclude[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%q is not an excluded name", name)
}

// SetMaxDepth sets the scan depth (1..12).
func (c *Config) SetMaxDepth(n int) error {
	if n < 1 || n > 12 {
		return errors.New("max depth must be between 1 and 12")
	}
	c.MaxDepth = n
	return nil
}

// AgentRule keeps an agent away from a folder, e.g. a client whose policy
// forbids it: {"path": "~/code/client", "deny": ["claude"], "reason": "..."}.
type AgentRule struct {
	Path   string   `json:"path"`
	Deny   []string `json:"deny"`
	Reason string   `json:"reason,omitempty"`
}

// AgentDenied reports whether a rule in "agentRules" forbids agent in dir,
// and the rule's reason.
func (c *Config) AgentDenied(agent, dir string) (bool, string) {
	var rules []AgentRule
	if raw, ok := c.extra["agentRules"]; ok {
		_ = json.Unmarshal(raw, &rules)
	}
	for _, r := range rules {
		p, err := resolve(r.Path)
		if err != nil || !Inside(p, dir) {
			continue
		}
		for _, d := range r.Deny {
			if d == agent {
				reason := r.Reason
				if reason == "" {
					reason = "not allowed under " + r.Path
				}
				return true, reason
			}
		}
	}
	return false, ""
}

// Terminal is the "terminal" key: warp, iterm or terminal ("" = detect).
func (c *Config) Terminal() string {
	var t string
	if raw, ok := c.extra["terminal"]; ok {
		_ = json.Unmarshal(raw, &t)
	}
	return t
}
