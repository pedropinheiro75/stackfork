package live

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Pause kinds.
const (
	PauseFailed  = "failed"  // a live run ended failed or interrupted
	PauseTrust   = "trust"   // the plan's host or prepare steps are not trusted here
	PauseRate    = "rate"    // 12 live runs in the last hour
	PausePlan    = "plan"    // the plan or its live overrides cannot be used
	PauseRefused = "refused" // the run could not begin for a reason other than a held lock
)

// Pause is why live mode stopped for a repository (design D9). While it
// exists the loop polls but starts no run and no fast-forward.
type Pause struct {
	Kind   string    `json:"kind"`
	Reason string    `json:"reason"` // e.g. "run 23 failed at step rebuild"
	Run    int       `json:"run,omitempty"`
	Step   string    `json:"step,omitempty"`
	Diff   []string  `json:"diff,omitempty"` // PauseTrust: the step diff
	At     time.Time `json:"at"`
}

// PauseFile is <stateBase>/live/<sha256 of the main checkout path>.json.
func PauseFile(stateBase, main string) string {
	sum := sha256.Sum256([]byte(main))
	return filepath.Join(stateBase, "live", hex.EncodeToString(sum[:])+".json")
}

// ReadPause returns (nil, nil) when the repository is not paused.
func ReadPause(stateBase, main string) (*Pause, error) {
	b, err := os.ReadFile(PauseFile(stateBase, main))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Pause
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// WritePause stores p (0600).
func WritePause(stateBase, main string, p Pause) error {
	path := PauseFile(stateBase, main)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return state.WritePrivate(path, p)
}

// Resume clears the pause (`stackfork live on` and **Resume**). The changes
// that waited are still pending against the baseline, so the next reconcile
// runs them once.
func Resume(stateBase, main string) error {
	err := os.Remove(PauseFile(stateBase, main))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func failedReason(id int, outcome, step string) string {
	if step == "" {
		return fmt.Sprintf("run %d %s", id, outcome)
	}
	return fmt.Sprintf("run %d %s at step %s", id, outcome, step)
}
