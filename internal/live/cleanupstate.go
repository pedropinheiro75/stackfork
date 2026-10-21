package live

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// maxHistory is the number of cleanup events a repository keeps (design D19).
const maxHistory = 20

// CleanupState is the repository's cleanup memory, in
// <stateBase>/live/<sha256 of the main checkout path>.cleanup.json (0600):
// the branches a person chose to keep, when each candidate first became
// eligible (the grace period starts there) and the last cleanups.
type CleanupState struct {
	Keep     []KeepEntry     `json:"keep,omitempty"`
	Eligible []EligibleEntry `json:"eligible,omitempty"`
	History  []HistoryEvent  `json:"history,omitempty"`
}

// KeepEntry: this branch at this head commit is not suggested or cleaned.
type KeepEntry struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

// EligibleEntry: the branch, at this head commit, has been eligible since.
type EligibleEntry struct {
	Branch string    `json:"branch"`
	Head   string    `json:"head"`
	Since  time.Time `json:"since"`
}

// HistoryEvent is one cleanup, with or without a recorded run.
type HistoryEvent struct {
	At     time.Time `json:"at"`
	Branch string    `json:"branch"`
	Path   string    `json:"path"`
	Result string    `json:"result"` // cleaned or failed
	Run    int       `json:"run,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// CleanupStateFile is the file of one repository.
func CleanupStateFile(stateBase, main string) string {
	sum := sha256.Sum256([]byte(main))
	return filepath.Join(stateBase, "live", hex.EncodeToString(sum[:])+".cleanup.json")
}

var cleanupStateMu sync.Mutex // one dashboard process: the loop and the API share the file

// LoadCleanupState returns an empty state when the file does not exist.
func LoadCleanupState(stateBase, main string) (CleanupState, error) {
	cleanupStateMu.Lock()
	defer cleanupStateMu.Unlock()
	return loadCleanupState(stateBase, main)
}

func loadCleanupState(stateBase, main string) (CleanupState, error) {
	var s CleanupState
	b, err := os.ReadFile(CleanupStateFile(stateBase, main))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// UpdateCleanupState reads, changes and writes the file under one lock; fn
// returns false to skip the write.
func UpdateCleanupState(stateBase, main string, fn func(*CleanupState) bool) error {
	cleanupStateMu.Lock()
	defer cleanupStateMu.Unlock()
	s, err := loadCleanupState(stateBase, main)
	if err != nil {
		return err
	}
	if !fn(&s) {
		return nil
	}
	path := CleanupStateFile(stateBase, main)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return state.WritePrivate(path, s)
}

// Kept reports whether the branch at head was kept by a person.
func (s CleanupState) Kept(branch, head string) bool {
	for _, k := range s.Keep {
		if k.Branch == branch && k.Head == head {
			return true
		}
	}
	return false
}

// KeepBranch stores a person's choice. A new head commit makes the branch a
// candidate again: the older entry for the branch is replaced, not kept.
func KeepBranch(stateBase, main, branch, head string) error {
	return UpdateCleanupState(stateBase, main, func(s *CleanupState) bool {
		keep := []KeepEntry{{Branch: branch, Head: head}}
		for _, k := range s.Keep {
			if k.Branch != branch {
				keep = append(keep, k)
			}
		}
		s.Keep = keep
		return true
	})
}

// since returns when the branch at head first became eligible, if it is recorded.
func (s CleanupState) since(branch, head string) (time.Time, bool) {
	for _, e := range s.Eligible {
		if e.Branch == branch && e.Head == head {
			return e.Since, true
		}
	}
	return time.Time{}, false
}

// RecordCleanup adds an event (the newest 20 stay) and forgets the branch's
// eligibility and keep entries.
func RecordCleanup(stateBase, main string, ev HistoryEvent) error {
	return UpdateCleanupState(stateBase, main, func(s *CleanupState) bool {
		s.History = append(s.History, ev)
		if n := len(s.History); n > maxHistory {
			s.History = s.History[n-maxHistory:]
		}
		if ev.Result == "cleaned" {
			s.Eligible = dropBranch(s.Eligible, ev.Branch, func(e EligibleEntry) string { return e.Branch })
			s.Keep = dropBranch(s.Keep, ev.Branch, func(e KeepEntry) string { return e.Branch })
		}
		return true
	})
}

func dropBranch[T any](list []T, branch string, name func(T) string) []T {
	var out []T
	for _, e := range list {
		if name(e) != branch {
			out = append(out, e)
		}
	}
	return out
}
