package runs

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// ErrNotFound is returned for an unknown run or step.
var ErrNotFound = errors.New("run or step not found")

// derive adds what readers report and the file does not store: `interrupted`
// when a `running` run has no lock holder (D7), and duration_ms.
func derive(r *Run) {
	if r.Outcome == Running && !state.Held(lockPath(r.dir)) {
		markInterrupted(r)
	}
	r.DurationMS = ms(r.StartedAt, r.EndedAt, r.Outcome == Running)
	for _, s := range r.Steps {
		s.DurationMS = ms(s.StartedAt, s.EndedAt, s.State == StepRunning)
	}
}

// ms is end-start, or start-to-now while still running, or 0 when unknown.
func ms(start, end time.Time, live bool) int64 {
	switch {
	case start.IsZero():
		return 0
	case !end.IsZero():
		return end.Sub(start).Milliseconds()
	case live:
		return time.Since(start).Milliseconds()
	}
	return 0
}

// Get returns one run for reading.
func Get(dir string, id int) (*Run, error) {
	r, err := load(dir, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	derive(r)
	return r, nil
}

// List returns the runs of an instance, newest first. Unreadable files are skipped.
func List(dir string) ([]*Run, error) {
	ids := listIDs(dir)
	slices.Reverse(ids)
	var out []*Run
	for _, id := range ids {
		if r, err := Get(dir, id); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// Log returns a step's log. The path comes from the stored run, never from step.
func Log(dir string, id int, step string) ([]byte, error) {
	r, err := Get(dir, id)
	if err != nil {
		return nil, err
	}
	for _, s := range r.Steps {
		if s.ID == step && s.Log != "" {
			return os.ReadFile(filepath.Join(runsDir(dir), filepath.FromSlash(s.Log)))
		}
	}
	return nil, ErrNotFound
}
