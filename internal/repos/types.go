package repos

import (
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// Repo is one git repository under a root (design.md D4).
type Repo struct {
	Name            string     `json:"name"`
	Path            string     `json:"path"`
	Group           string     `json:"group"` // path relative to its root, minus the repo name
	DefaultBranch   string     `json:"default_branch"`
	Readiness       string     `json:"readiness"` // ready, needs-learn, draft, unknown
	ReadinessReason string     `json:"readiness_reason"`
	Worktrees       []Worktree `json:"worktrees"` // includes the main checkout, first
	Branches        []Branch   `json:"branches"`  // local branches with no worktree
	Running         int        `json:"running"`
	LastCommit      time.Time  `json:"last_commit"`
}

// Worktree is a checkout of the repo. Instance is the stackfork instance
// prepared for it, if any.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Main   bool   `json:"main,omitempty"`
	Dirty  bool   `json:"dirty"`
	// Ahead and Behind count commits against Upstream (what git tracks for
	// the branch), not against the default branch; both are 0 without one.
	Ahead      int                    `json:"ahead"`
	Behind     int                    `json:"behind"`
	Upstream   string                 `json:"upstream,omitempty"`
	LastCommit time.Time              `json:"last_commit"`
	Instance   *inventory.InstanceRef `json:"instance,omitempty"`
}

// Branch is a local branch with no worktree.
type Branch struct {
	Name       string    `json:"name"`
	LastCommit time.Time `json:"last_commit"`
	Ahead      int       `json:"ahead"`
	Behind     int       `json:"behind"`
	Upstream   string    `json:"upstream,omitempty"`
}

// Readiness levels.
const (
	Ready      = "ready"
	NeedsLearn = "needs-learn" // it could run (adapter, sibling, profile) but nothing verified how
	Draft      = "draft"
	Unknown    = "unknown"
)

// gitData is everything read with git for one repo; it is what the cache
// stores (readiness and instance state are recomputed on every scan).
type gitData struct {
	DefaultBranch string     `json:"default_branch"`
	Worktrees     []Worktree `json:"worktrees"`
	Branches      []Branch   `json:"branches"`
	LastCommit    time.Time  `json:"last_commit"`
}
