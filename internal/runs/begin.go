package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// ErrBusy is wrapped by the error Begin returns when another run holds the lock.
var ErrBusy = errors.New("another run is in progress for this instance")

// ponytail: constants and a test-tunable wait; see design D5 and D6.
const keepRuns = 20

var lockWait = 2 * time.Second

func lockPath(dir string) string { return filepath.Join(dir, ".run.lock") }

// Begin takes the instance's run lock (retrying for up to 2 s, or once with
// Meta.NoWait), marks the runs of dead processes interrupted, assigns the next
// id, applies retention and writes the new run. Call Finish to release the lock.
func Begin(dir string, m Meta) (*Run, error) {
	if err := os.MkdirAll(runsDir(dir), 0o700); err != nil {
		return nil, err
	}
	var lock *state.Lock
	deadline := time.Now().Add(lockWait)
	for {
		l, err := state.TryLock(lockPath(dir))
		if err == nil {
			lock = l
			break
		}
		if !errors.Is(err, state.ErrLocked) {
			return nil, err
		}
		if m.NoWait || time.Now().After(deadline) {
			return nil, busyError(dir)
		}
		time.Sleep(50 * time.Millisecond)
	}
	ids := listIDs(dir)
	for _, id := range ids {
		// We hold the lock, so no stored `running` run has an owner.
		if r, err := load(dir, id); err == nil && r.Outcome == Running {
			markInterrupted(r)
			r.EndedAt = now()
			r.save()
		}
	}
	next := 1
	if n := len(ids); n > 0 {
		next = ids[n-1] + 1
	}
	r, err := newRun(dir, m, next)
	if err != nil {
		lock.Release()
		return nil, err
	}
	r.lock = lock
	ids = append(ids, next)
	for len(ids) > keepRuns {
		os.Remove(filepath.Join(runsDir(dir), itoa(ids[0])+".json"))
		os.RemoveAll(filepath.Join(runsDir(dir), itoa(ids[0])))
		ids = ids[1:]
	}
	return r, nil
}

func busyError(dir string) error {
	ids := listIDs(dir)
	for i := len(ids) - 1; i >= 0; i-- {
		if r, err := load(dir, ids[i]); err == nil && r.Outcome == Running {
			return fmt.Errorf("%w (run %d, %s from %s, started %d s ago)", ErrBusy, r.ID, r.Command, r.Trigger,
				int(time.Since(r.StartedAt).Seconds()))
		}
	}
	return ErrBusy
}

// markInterrupted applies the D7 result to r in memory.
func markInterrupted(r *Run) {
	r.Outcome = Interrupted
	for _, s := range r.Steps {
		if s.State == StepRunning {
			s.State, s.Reason = Failed, "interrupted"
		}
	}
}

// listIDs returns the ids that have a <id>.json file, ascending.
func listIDs(dir string) []int {
	ents, _ := os.ReadDir(runsDir(dir))
	var ids []int
	for _, e := range ents {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if n, err := strconv.Atoi(name); ok && err == nil && n > 0 && !e.IsDir() {
			ids = append(ids, n)
		}
	}
	sort.Ints(ids)
	return ids
}

// load reads one run file. The result has no lock and is for reading.
func load(dir string, id int) (*Run, error) {
	b, err := os.ReadFile(filepath.Join(runsDir(dir), itoa(id)+".json"))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Schema != schema || r.ID != id {
		return nil, fmt.Errorf("run %d: unsupported schema %d", id, r.Schema)
	}
	r.dir = dir
	for i, s := range r.Steps {
		s.run, s.index = &r, i+1
	}
	return &r, nil
}
