package live

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// BaselineFile is the baseline's name in an instance directory (design D7).
const BaselineFile = "live.json"

// TriggerLive is the trigger label of a live run (add-live-mode D10).
const TriggerLive = "live"

// Baseline is what "changed" is compared with: the branch, the upstream
// commit last seen, and the SHA-256 of every watched path.
type Baseline struct {
	Branch   string            `json:"branch"`
	Upstream string            `json:"upstream,omitempty"`
	Digests  map[string]string `json:"digests"`
}

// LoadBaseline returns (nil, nil) when the instance has none yet.
func LoadBaseline(instanceDir string) (*Baseline, error) {
	b, err := os.ReadFile(filepath.Join(instanceDir, BaselineFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var bl Baseline
	if err := json.Unmarshal(b, &bl); err != nil {
		return nil, err
	}
	if bl.Digests == nil {
		bl.Digests = map[string]string{}
	}
	return &bl, nil
}

func (b *Baseline) save(instanceDir string) error {
	return state.WritePrivate(filepath.Join(instanceDir, BaselineFile), b)
}

// FirstSight loads the baseline, or writes one from the poller's current
// digests when the target has none: live mode on, a dashboard start with no
// baseline, a new instance. created tells which. Call Poll first.
func FirstSight(instanceDir, branch, upstream string, p *Poller) (b *Baseline, created bool, err error) {
	if b, err = LoadBaseline(instanceDir); b != nil || err != nil {
		return b, false, err
	}
	b = &Baseline{Branch: branch, Upstream: upstream, Digests: p.Digests()}
	return b, true, b.save(instanceDir)
}

// RecordRun moves the baseline after a run, for the covered changes only.
// Only a succeeded live run moves it: a failed one leaves the change pending,
// and `cli` and `dashboard` runs never hide a change from live mode.
// upstream is the commit the run reconciled to ("" keeps the stored one).
func (b *Baseline) RecordRun(instanceDir, trigger, outcome string, covered []Change, upstream string) error {
	if trigger != TriggerLive || outcome != runs.Succeeded {
		return nil
	}
	for _, c := range covered {
		if c.Kind == "removed" {
			delete(b.Digests, c.Path)
		} else {
			b.Digests[c.Path] = c.Digest
		}
	}
	if upstream != "" {
		b.Upstream = upstream
	}
	return b.save(instanceDir)
}

// ReadBranch reads the checked-out branch from the checkout's HEAD file,
// following the `gitdir:` line of a worktree's .git file. detached is true
// when HEAD holds a commit.
func ReadBranch(checkout string) (branch string, detached bool, err error) {
	gitPath := filepath.Join(checkout, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return "", false, err
	}
	dir := gitPath
	if !fi.IsDir() {
		b, err := os.ReadFile(gitPath)
		if err != nil {
			return "", false, err
		}
		rest, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
		if !ok {
			return "", false, errors.New(".git file has no gitdir line")
		}
		dir = rest
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(checkout, dir)
		}
	}
	h, err := os.ReadFile(filepath.Join(dir, "HEAD"))
	if err != nil {
		return "", false, err
	}
	if name, ok := strings.CutPrefix(strings.TrimSpace(string(h)), "ref: refs/heads/"); ok {
		return name, false, nil
	}
	return "", true, nil
}
