package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/runs"
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
	// Instance is the candidate's instance, nil when it has none.
	Instance *Target
	// ActionLocked tells whether the dashboard's per-project action lock is
	// held for the instance; nil means never.
	ActionLocked func(project string) bool
	// Down removes the instance's containers and volumes (`stackfork
	// --instance P down --volumes`). The CLI provides it; nil with an Instance
	// fails the step.
	Down func(w io.Writer) error
	Out  io.Writer        // step output, may be nil
	Now  func() time.Time // nil: time.Now
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
	return cleanup.Check{Main: j.Main, Candidate: j.Candidate, Result: j.Result, Auto: j.Auto, Adapter: j.Adapter,
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
	out.Verdict = cleanup.Evaluate(j.CheckFor())
	if !out.Verdict.Eligible {
		out.Err = fmt.Errorf("%w: %s", ErrNotEligible, out.Verdict.Warning)
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
