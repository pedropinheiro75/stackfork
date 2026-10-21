package config

import (
	"fmt"
	"sort"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// Cleanup modes and bounds (live-mode D15).
const (
	CleanupOff     = "off"
	CleanupSuggest = "suggest"
	CleanupAuto    = "auto"

	MinGraceHours     = 1
	MaxGraceHours     = 720
	DefaultGraceHours = 24
)

// Cleanup is the branch-cleanup setting of one repository.
type Cleanup struct {
	Mode         string `json:"mode"`
	GraceHours   int    `json:"grace_hours"`
	DeleteBranch bool   `json:"delete_branch"`
}

func (c Cleanup) validate() error {
	switch c.Mode {
	case CleanupOff, CleanupSuggest, CleanupAuto:
	default:
		return fmt.Errorf("unknown cleanup mode %q (%s, %s or %s)", c.Mode, CleanupOff, CleanupSuggest, CleanupAuto)
	}
	if c.GraceHours < MinGraceHours || c.GraceHours > MaxGraceHours {
		return fmt.Errorf("grace_hours must be between %d and %d", MinGraceHours, MaxGraceHours)
	}
	return nil
}

func (c *Config) validateLive() error {
	repos := make([]string, 0, len(c.Cleanup))
	for r := range c.Cleanup {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, r := range repos {
		if err := c.Cleanup[r].validate(); err != nil {
			return fmt.Errorf("cleanup for %s: %w", r, err)
		}
	}
	return nil
}

func liveKey(repo string) string {
	if p, err := identity.ResolvePath(repo); err == nil {
		return p
	}
	return repo
}

// LiveFor reports whether live mode is on for a repository (off when unset).
func (c *Config) LiveFor(repo string) bool { return c.Live[liveKey(repo)] }

// SetLive turns live mode on for a repository, keyed by its resolved path.
func (c *Config) SetLive(repo string) error {
	p, err := identity.ResolvePath(repo)
	if err != nil {
		return err
	}
	if c.Live == nil {
		c.Live = map[string]bool{}
	}
	c.Live[p] = true
	return nil
}

// UnsetLive turns live mode off; the key is removed.
func (c *Config) UnsetLive(repo string) { delete(c.Live, liveKey(repo)) }

// LiveRepos lists the repositories with live mode on, sorted.
func (c *Config) LiveRepos() []string {
	var out []string
	for r, on := range c.Live {
		if on {
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}

// CleanupFor is the cleanup setting of a repository. Without an entry it is
// suggest/24 h/keep branch when live mode is on, and off otherwise.
func (c *Config) CleanupFor(repo string) Cleanup {
	if cl, ok := c.Cleanup[liveKey(repo)]; ok {
		return cl
	}
	if c.LiveFor(repo) {
		return Cleanup{Mode: CleanupSuggest, GraceHours: DefaultGraceHours}
	}
	return Cleanup{Mode: CleanupOff, GraceHours: DefaultGraceHours}
}

// SetCleanup stores a validated cleanup setting under the resolved path.
func (c *Config) SetCleanup(repo string, cl Cleanup) error {
	if err := cl.validate(); err != nil {
		return err
	}
	p, err := identity.ResolvePath(repo)
	if err != nil {
		return err
	}
	if c.Cleanup == nil {
		c.Cleanup = map[string]Cleanup{}
	}
	c.Cleanup[p] = cl
	return nil
}
