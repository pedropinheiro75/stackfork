// Package runs records one execution of a plan: which steps ran, how each
// ended, and what each printed (design.md of add-plan-runs).
//
// API choices the design left open:
//   - Meta.Out is the caller's output (terminal or dashboard stream); every
//     step writer feeds it and the step log through the redactor.
//   - Meta.Secrets is called each time a step starts, so a step after
//     `prepare` sees the secrets `prepare` just recorded.
//   - Meta.Observer receives an Event per run or step state change, after
//     the run file is written.
//   - (*Run).Step declares a step as pending and is idempotent per id.
//   - (*Step).End(err, exitCode): err != nil marks the step failed; a
//     positive exitCode is stored as exit_code.
//   - (*Run).SetRetry stores the command that retries a failed run; Finish(err)
//     stores err.Error() as the run's error. Finish is idempotent.
//   - Write failures of run files and logs never fail the command being
//     recorded: the record is diagnostics, not data.
//   - A step log is complete on disk when the step ends: the part after the
//     first 512 KiB is kept in memory until then.
//   - Readers (List, Get) return copies with the derived `interrupted`.
package runs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Run outcomes and step states.
const (
	Running     = "running"     // run outcome; also see StepRunning
	Succeeded   = "succeeded"   // run outcome and step state
	Failed      = "failed"      // run outcome and step state
	Interrupted = "interrupted" // run outcome, derived from the run lock
	Pending     = "pending"     // step state
	StepRunning = "running"     // step state
	Skipped     = "skipped"     // step state
)

const schema = 1

// Meta is what the caller knows when a run begins.
type Meta struct {
	executionProofs map[string]string
	RecipeHash      string
	Command         string // start-env, up, seed, reset or live
	Trigger         string // cli, dashboard or live
	Cause           *Cause // live runs only: why the run started
	NoWait          bool   // Begin tries the run lock once instead of retrying for 2 s
	Mode            string
	Project         string
	Branch          string
	Out             io.Writer              // caller output, may be nil
	Secrets         func() []redact.Secret // may be nil
	Observer        func(Event)            // may be nil
}

// Event describes one state change. Step is empty for a run-level change.
type Event struct {
	Run     int
	Project string
	Step    string
	Kind    string // step kind, set on the pending event that declares the step
	State   string // step state, or the run outcome when Step is empty
	Reason  string
}

// Run is one execution of a plan, as stored in runs/<id>.json.
type Run struct {
	ExecutionProofs map[string]string `json:"execution_proofs,omitempty"`
	RecipeHash      string            `json:"recipe_hash,omitempty"`
	SnapshotHash    string            `json:"snapshot_hash,omitempty"`
	Schema          int               `json:"schema"`
	ID              int               `json:"id"`
	Command         string            `json:"command"`
	Trigger         string            `json:"trigger"`
	Mode            string            `json:"mode"`
	Project         string            `json:"project"`
	Branch          string            `json:"branch"`
	PID             int               `json:"pid"`
	StartedAt       time.Time         `json:"started_at"`
	EndedAt         time.Time         `json:"ended_at,omitzero"`
	Outcome         string            `json:"outcome"`
	Error           string            `json:"error,omitempty"`
	Retry           string            `json:"retry,omitempty"`
	Cause           *Cause            `json:"cause,omitempty"`
	Steps           []*Step           `json:"steps"`
	// DurationMS is set by readers only; it is never in a stored file.
	DurationMS int64 `json:"duration_ms,omitempty"`

	dir  string // instance directory
	meta Meta
	mu   sync.Mutex
	lock *state.Lock
}

// Step is one entry of a run.
type Step struct {
	DefinitionHash string    `json:"definition_hash,omitempty"`
	ExecutedHash   string    `json:"executed_hash,omitempty"`
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Text           string    `json:"text"`
	State          string    `json:"state"`
	Reason         string    `json:"reason,omitempty"`
	StartedAt      time.Time `json:"started_at,omitzero"`
	EndedAt        time.Time `json:"ended_at,omitzero"`
	Log            string    `json:"log,omitempty"` // relative to runs/, e.g. 17/seed.log
	ExitCode       *int      `json:"exit_code,omitempty"`
	// DurationMS is set by readers only; it is never in a stored file.
	DurationMS int64 `json:"duration_ms,omitempty"`

	run   *Run
	index int // 1-based position, for the log name fallback
	w     *stepWriter
}

func itoa(n int) string { return strconv.Itoa(n) }

func runsDir(dir string) string { return filepath.Join(dir, "runs") }

func now() time.Time { return time.Now().UTC() }

// newRun creates runs/<id>.json. Begin adds the lock, id choice and sweeps.
func newRun(dir string, m Meta, id int) (*Run, error) {
	if err := os.MkdirAll(runsDir(dir), 0o700); err != nil {
		return nil, err
	}
	r := &Run{Schema: schema, ID: id, Command: m.Command, Trigger: m.Trigger, Mode: m.Mode, RecipeHash: m.RecipeHash,
		ExecutionProofs: m.executionProofs,
		Project:         m.Project, Branch: m.Branch, PID: os.Getpid(), StartedAt: now(),
		Outcome: Running, Cause: m.Cause, Steps: []*Step{}, dir: dir, meta: m}
	if err := r.save(); err != nil {
		return nil, err
	}
	return r, nil
}

// save writes the run file. The caller holds r.mu, or owns r alone.
func (r *Run) save() error {
	return state.WritePrivate(filepath.Join(runsDir(r.dir), itoa(r.ID)+".json"), r)
}

// update applies fn under the lock, saves, then tells the observer.
func (r *Run) update(ev Event, fn func()) {
	r.mu.Lock()
	fn()
	r.save() // ponytail: write errors are dropped on purpose, see package doc
	r.mu.Unlock()
	if r.meta.Observer != nil {
		ev.Project = r.Project
		r.meta.Observer(ev)
	}
}

// SetRetry stores the command that retries a failed run.
func (r *Run) SetRetry(cmd string) {
	r.update(Event{Run: r.ID, State: r.Outcome}, func() { r.Retry = cmd })
}

// SetMode replaces the mode a run began with; a reset learns it only after sync.
func (r *Run) SetMode(mode string) {
	r.update(Event{Run: r.ID, State: r.Outcome}, func() { r.Mode = mode })
}

func (r *Run) SetRecipeHash(hash string) {
	r.update(Event{Run: r.ID, State: r.Outcome}, func() { r.RecipeHash = hash })
}

func (r *Run) SetSnapshotHash(hash string) {
	r.update(Event{Run: r.ID, State: r.Outcome}, func() { r.SnapshotHash = hash })
}

func (r *Run) ExecutedDefinition(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ExecutionProofs[r.Mode+":"+id]
}

// Step declares a step as pending, or returns the one already declared.
func (r *Run) Step(id, kind, text string) *Step {
	r.mu.Lock()
	for _, s := range r.Steps {
		if s.ID == id {
			r.mu.Unlock()
			return s
		}
	}
	s := &Step{ID: id, Kind: kind, Text: text, State: Pending, run: r, index: len(r.Steps) + 1}
	r.Steps = append(r.Steps, s)
	r.save()
	r.mu.Unlock()
	if r.meta.Observer != nil {
		r.meta.Observer(Event{Run: r.ID, Project: r.Project, Step: id, Kind: kind, State: Pending})
	}
	return s
}

// Finish ends the run: succeeded, or failed with err's text. It releases the
// run lock. A second call does nothing.
func (r *Run) Finish(err error) {
	r.mu.Lock()
	if r.Outcome != Running {
		r.mu.Unlock()
		return
	}
	for _, s := range r.Steps { // a step that was never ended still holds its log open
		if s.w != nil {
			s.w.Close()
			s.w = nil
		}
	}
	r.mu.Unlock()
	out := Succeeded
	if err != nil {
		out = Failed
	}
	r.update(Event{Run: r.ID, State: out}, func() {
		r.Outcome, r.EndedAt = out, now()
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Retry = "" // a default set at Begin only serves interrupted and failed runs
		}
	})
	if r.lock != nil {
		r.lock.Release()
		r.lock = nil
	}
}

// logName is the file name for a step log: the step id, or step-<n> when the
// id holds a path separator (or nothing).
func (s *Step) logName() string { return logFileName(s.ID, s.index) }

func logFileName(id string, index int) string {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return fmt.Sprintf("step-%d.log", index)
	}
	return id + ".log"
}

// Start marks the step running and returns its writer: redactor, then the
// caller's output and the capped step log.
func (s *Step) Start() io.Writer {
	r := s.run
	var secrets []redact.Secret
	if r.meta.Secrets != nil {
		secrets = r.meta.Secrets()
	}
	dir := filepath.Join(runsDir(r.dir), itoa(r.ID))
	var logf *os.File
	if err := os.MkdirAll(dir, 0o700); err == nil {
		logf, _ = os.OpenFile(filepath.Join(dir, s.logName()), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	}
	s.w = newStepWriter(r.meta.Out, logf, secrets)
	r.update(Event{Run: r.ID, Step: s.ID, State: StepRunning}, func() {
		s.ExecutedHash = ""
		s.State, s.StartedAt = StepRunning, now()
		if logf != nil {
			s.Log = itoa(r.ID) + "/" + s.logName()
		}
	})
	return s.w
}

// End closes the step's writer and records the result.
func (s *Step) End(err error, exitCode int) {
	if s.w != nil {
		line := ""
		if err != nil { // the log, and the tail the card shows, end with why it failed
			line = "error: " + err.Error()
		}
		s.w.closeWith(line)
		s.w = nil
	}
	st := Succeeded
	if err != nil {
		st = Failed
	}
	s.run.update(Event{Run: s.run.ID, Step: s.ID, State: st}, func() {
		s.State, s.EndedAt = st, now()
		if err == nil {
			s.ExecutedHash = s.DefinitionHash
			if s.DefinitionHash != "" {
				if s.run.ExecutionProofs == nil {
					s.run.ExecutionProofs = map[string]string{}
				}
				s.run.ExecutionProofs[s.run.Mode+":"+s.ID] = s.DefinitionHash
			}
		}
		if err != nil && exitCode > 0 {
			s.ExitCode = &exitCode
		}
	})
}

func (s *Step) SetDefinition(hash, executed string) {
	s.run.update(Event{Run: s.run.ID, Step: s.ID, State: s.State}, func() {
		s.DefinitionHash, s.ExecutedHash = hash, executed
	})
}

// Skip records a step that was reached and not executed.
func (s *Step) Skip(reason string) {
	s.run.update(Event{Run: s.run.ID, Step: s.ID, State: Skipped, Reason: reason}, func() {
		t := now()
		s.State, s.Reason, s.StartedAt, s.EndedAt = Skipped, reason, t, t
	})
}
