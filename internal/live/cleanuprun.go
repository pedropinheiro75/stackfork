package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// CleanupJob is one branch cleanup (design D19): the candidate, how it was
// detected, the repository's setting and, when the worktree has an instance,
// what is needed to remove it.
type CleanupJob struct {
	Main, NewBase string
	Candidate     cleanup.Candidate
	Result        cleanup.Result // the detection that made it a candidate (State merged, PR, Forge)
	Settings      config.Cleanup
	// Auto: the cleanup starts without a click, so ignored files block it.
	Auto    bool
	Adapter []string // knowledge adapter_files
	// Own are the files of the instance's manifest that stackfork wrote into
	// the checkout (see ownFiles).
	Own []string
	// Instance is the candidate's instance, nil when it has none.
	Instance *Target
	// Others are the branches of other instances in the candidate's worktree:
	// removing the worktree would orphan them, so the cleanup is refused.
	Others []string
	// ActionLocked tells whether the dashboard's per-project action lock is
	// held for the instance; nil means never.
	ActionLocked func(project string) bool
	// Down removes the instance's containers and volumes (`stackfork
	// --instance P down --volumes`). The CLI provides it; nil with an Instance
	// fails the step.
	Down func(w io.Writer) error
	// Confirm, when set, sees the fresh verdict after the eligibility
	// re-check and before any step; an error refuses the cleanup. A page uses
	// it to refuse when the ignored files or the volumes grew since the person
	// confirmed.
	Confirm func(cleanup.Verdict) error
	Out     io.Writer        // step output, may be nil
	Now     func() time.Time // nil: time.Now
}

// ErrCleanupRunning is the refusal of a cleanup that finds another one running.
var ErrCleanupRunning = errors.New("a cleanup is already running for this repository")

// CleanupLockFile is <stateBase>/live/<hash of the main checkout>.cleanup.lock.
func CleanupLockFile(stateBase, main string) string {
	return strings.TrimSuffix(PauseFile(stateBase, main), ".json") + ".cleanup.lock"
}

func tryCleanupLock(stateBase, main string) (*state.Lock, error) {
	path := CleanupLockFile(stateBase, main)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := state.TryLock(path)
	if errors.Is(err, state.ErrLocked) {
		return nil, ErrCleanupRunning
	}
	return lock, err
}

// ErrNotEligible wraps the warning of a refused re-check. Nothing ran.
var ErrNotEligible = errors.New("not eligible for cleanup")

// CleanupOutcome is what RunCleanup did.
type CleanupOutcome struct {
	Verdict cleanup.Verdict
	Run     RunResult // ID is 0 for a candidate without an instance, or a refusal
	Pause   *Pause    // set when this call paused live mode
	Err     error
}

// Cause is the run's cause record: the branch, "merged", the pull request
// when gh knew it, and git or git+gh.
func (j CleanupJob) cause() *runs.Cause {
	c := &runs.CauseCleanup{Branch: j.Candidate.Branch, Result: cleanup.Merged, PR: j.Result.PR, Via: "git"}
	if j.Result.PR != 0 {
		c.Via = "git+gh"
	}
	return &runs.Cause{Cleanup: c}
}

func (j CleanupJob) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// CheckFor is the eligibility question for this job.
func (j CleanupJob) CheckFor() cleanup.Check {
	return cleanup.Check{Main: j.Main, Candidate: j.Candidate, Result: j.Result, Auto: j.Auto, Adapter: j.Adapter, Own: j.Own, Others: j.Others,
		Locked: func() string {
			if j.Instance == nil {
				return ""
			}
			if runs.Busy(j.Instance.InstanceDir) {
				return "a run holds the instance's run lock"
			}
			if j.ActionLocked != nil && j.ActionLocked(j.Instance.Project) {
				return "a dashboard action is running for this instance"
			}
			return ""
		}}
}

// RunCleanup re-checks eligibility, then removes the worktree (never
// --force: git refuses a worktree with changes), then the instance with its
// volumes, then the local branch when the setting says so. A candidate with
// an instance is recorded as a live run with a cleanup cause; one without is
// kept in the repository's cleanup history. A failed step stops the run and
// pauses live mode for the repository (design D9, D19).
func RunCleanup(ctx context.Context, j CleanupJob) CleanupOutcome {
	var out CleanupOutcome
	// The CLI, the page and the loop all come through here: one cleanup at a
	// time per repository, so two cannot race on the same worktree or branch.
	lock, lerr := tryCleanupLock(j.NewBase, j.Main)
	if lerr != nil {
		out.Err = lerr
		return out
	}
	defer lock.Release()
	out.Verdict = cleanup.Evaluate(j.CheckFor())
	if !out.Verdict.Eligible {
		out.Err = fmt.Errorf("%w: %s", ErrNotEligible, out.Verdict.Warning)
		return out
	}
	// Keep is read from disk now: the candidate may come from a scan that is
	// older than the person's click. It stops an automatic cleanup; a person
	// who asks for this branch explicitly is not stopped by their own Keep.
	if j.Confirm != nil {
		if err := j.Confirm(out.Verdict); err != nil {
			out.Err = fmt.Errorf("%w: %v", ErrNotEligible, err)
			return out
		}
	}
	if st, _ := LoadCleanupState(j.NewBase, j.Main); j.Auto && st.Kept(j.Candidate.Branch, j.Candidate.Head) {
		out.Err = fmt.Errorf("%w: %s is kept", ErrNotEligible, j.Candidate.Branch)
		return out
	}
	if err := ctx.Err(); err != nil {
		out.Err = err
		return out
	}
	br, path := j.Candidate.Branch, j.Candidate.Path
	target := cleanup.Target(j.Main)

	var rec *runs.Run
	if j.Instance != nil {
		var err error
		rec, err = runs.Begin(j.Instance.InstanceDir, runs.Meta{Command: "live", Trigger: TriggerLive, NoWait: true, Cause: j.cause(),
			Mode: lastMode(j.Instance.InstanceDir), Project: j.Instance.Project, Branch: br, Out: j.Out})
		if err != nil {
			out.Err = err
			return out
		}
		out.Run.ID = rec.ID
		rec.SetRetry("stackfork live clean " + j.Main + " " + br + " --yes")
	}
	type step struct {
		id, kind, text string
		run            func(w io.Writer) error
	}
	steps := []step{{"remove-worktree", "git", "git worktree remove " + path, func(w io.Writer) error {
		o, err := cleanup.RemoveWorktree(j.Main, path)
		fmt.Fprintln(w, o)
		return err
	}}}
	if j.Instance != nil {
		steps = append(steps, step{"down", "down", "remove the containers and delete the volumes", func(w io.Writer) error {
			if j.Down == nil {
				return errors.New("no way to remove the instance")
			}
			return j.Down(w)
		}})
	}
	if j.Settings.DeleteBranch {
		steps = append(steps, step{"delete-branch", "git", "git branch -d " + br, func(w io.Writer) error {
			o, err := cleanup.DeleteBranch(j.Main, br, target)
			fmt.Fprintln(w, o)
			return err
		}})
	}
	var recSteps []*runs.Step
	if rec != nil {
		for _, s := range steps {
			recSteps = append(recSteps, rec.Step(s.id, s.kind, s.text))
		}
	}
	var failedStep string
	var err error
	for i, s := range steps {
		var w io.Writer = io.Discard
		if rec != nil {
			w = recSteps[i].Start()
		}
		err = s.run(w)
		if rec != nil {
			recSteps[i].End(err, 0)
		}
		if err != nil {
			failedStep = s.id
			break
		}
		if s.id == "remove-worktree" && rec != nil { // the checkout is gone: the retry is the orphan down
			rec.SetRetry("stackfork --instance " + j.Instance.Project + " down --volumes")
		}
	}
	ev := HistoryEvent{At: j.now().UTC(), Branch: br, Path: path, Result: "cleaned", Run: out.Run.ID}
	if rec != nil {
		rec.Finish(err)
		if r, gerr := runs.Get(j.Instance.InstanceDir, rec.ID); gerr == nil {
			out.Run.Outcome = r.Outcome
		}
	}
	out.Run.Step = failedStep
	if err != nil {
		ev.Result, ev.Detail = "failed", failedStep+": "+err.Error()
		out.Err = err
		p := Pause{Kind: PauseFailed, Reason: fmt.Sprintf("cleanup of %s failed at step %s", br, failedStep), Run: out.Run.ID, Step: failedStep, At: j.now().UTC()}
		if werr := WritePause(j.NewBase, j.Main, p); werr == nil {
			out.Pause = &p
		}
	}
	RecordCleanup(j.NewBase, j.Main, ev)
	return out
}
