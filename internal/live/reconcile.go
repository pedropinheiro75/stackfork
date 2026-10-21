package live

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// Guardrails (design D9). ponytail: constants; make them configuration only
// if a project needs other values.
const (
	MinSpacing = 30 * time.Second // between the starts of two live runs of one instance
	HourlyMax  = 12               // live runs per repository per hour
)

// Target is one instance the reconciler may act on, with what changed in its
// checkout since the baseline. The loop (group 5) fills it: Targets lists the
// instances, the Poller gives Changes, the Fetcher gives Upstream.
type Target struct {
	Main        string // the repository's main checkout (the key of config.json `live`)
	Root        string // this instance's checkout
	Project     string // the instance name
	InstanceDir string
	Branch      string // the instance's branch (the manifest's)
	State       string // inventory state: running and partial are targets
	NewBase     string // the state base: trust file, pause file
	LegacyBase  string
	Global      []string // global flags to forward (--secret, --npmrc)
	Changes     []Change
	// Upstream is the fast-forward the loop did since the last reconcile
	// (ref, old and new commit, count); nil when the checkout did not move.
	Upstream *runs.CauseUpstream
}

// RunResult is what Exec reports about the run it recorded. ID is 0 when the
// run did not begin.
type RunResult struct {
	ID      int
	Outcome string // runs.Succeeded, runs.Failed or runs.Interrupted
	Step    string // the failed step
}

// Exec runs the job as a recorded live run. The CLI provides it (it drives
// StartEnv with StartOptions.Live); an error wrapping runs.ErrBusy means the
// run lock was held and nothing was recorded.
type Exec func(ctx context.Context, t Target, j Job) (RunResult, error)

// Statuses of an Outcome.
const (
	Idle    = "idle"    // nothing changed
	Ran     = "ran"     // a live run was recorded (see Run, Pause)
	Settled = "settled" // the changes need no step; the baseline moved
	Skipped = "skipped" // try again on a later tick; the change is still pending
	Waiting = "waiting" // not a target now (stopped, branch switched); the change is still pending
	Paused  = "paused"  // live mode is paused for the repository; nothing ran
)

// Outcome is the result of one Reconcile.
type Outcome struct {
	Status  string
	Reason  string   // why it was skipped, waiting or paused
	Warning string   // e.g. "branch switched"
	Notes   []string // what the run does not do (an excluded service, no schema step)
	Run     RunResult
	Pause   *Pause // set when this call paused live mode
}

// Reconciler holds what the guardrails remember between calls. One per
// dashboard; the zero value works once Exec is set.
type Reconciler struct {
	Exec Exec
	// ActionLocked tells whether the dashboard's per-project action lock is
	// held for the instance; nil means never.
	ActionLocked func(project string) bool
	Now          func() time.Time // nil: time.Now

	mu      sync.Mutex
	busy    bool                   // one live run at a time
	last    map[string]time.Time   // project -> start of its last live run
	started map[string][]time.Time // main -> starts of live runs in the last hour
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Reconcile is the entry point of the loop: it brings one target in line
// with the changes seen in its checkout, or says why it did not. It never
// queues: a held lock, a busy reconciler or the 30-second spacing give
// Skipped and the caller tries again on a later tick with the same changes.
//
// Order: paused? target? plan usable and trusted (HashTrusted in every mode)?
// anything to run? locks free? spacing and hourly limit? then one recorded
// run with trigger live, whose first step is the `secrets` preflight. A
// succeeded run moves the baseline for the changes it covered; a failed or
// interrupted one pauses the repository (design D9).
func (r *Reconciler) Reconcile(ctx context.Context, t Target) (Outcome, error) {
	if len(t.Changes) == 0 && t.Upstream == nil {
		return Outcome{Status: Idle}, nil
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if p, err := ReadPause(t.NewBase, t.Main); err != nil {
		return Outcome{}, err
	} else if p != nil {
		return Outcome{Status: Paused, Reason: p.Reason, Pause: nil}, nil
	}
	// D3: only running and partial instances; a switched branch is a warning.
	if t.State != inventory.StateRunning && t.State != inventory.StatePartial {
		return Outcome{Status: Waiting, Reason: "instance is " + t.State}, nil
	}
	if b, detached, err := ReadBranch(t.Root); err == nil && (detached || b != t.Branch) {
		return Outcome{Status: Waiting, Reason: "the checkout is not on " + t.Branch, Warning: "branch switched"}, nil
	}

	k, err := knowledge.LoadFirst(t.Root, t.Main)
	if err != nil {
		return r.pause(t, Pause{Kind: PausePlan, Reason: "knowledge.json: " + err.Error()})
	}
	var plan knowledge.Plan
	if k != nil {
		plan = k.Plan
	}
	mode := lastMode(t.InstanceDir)
	if mode == "" {
		mode = knowledge.ModeContainer
		if cfg, err := config.Load(); err == nil {
			mode, _ = cfg.ModeForPlan(t.Main, plan)
		}
	}
	// D12: no run before the plan is trusted here. HashTrusted looks at the
	// host and prepare steps in every mode, because nobody watches the plan
	// change; PlanRunner.Check adds the mode support and exec-service checks.
	if ok, _ := lifecycle.HashTrusted(t.NewBase, t.Main, plan); !ok {
		return r.pause(t, Pause{Kind: PauseTrust, Reason: "plan needs trust", Diff: lifecycle.ReadableDiff(t.NewBase, t.Main, plan)})
	}
	check := lifecycle.PlanRunner{NewBase: t.NewBase, Main: t.Main, Root: t.Root, InstanceDir: t.InstanceDir, Mode: mode, Plan: plan}
	if err := check.Check(); err != nil {
		return r.pause(t, Pause{Kind: PausePlan, Reason: err.Error()})
	}

	bl, err := LoadBaseline(t.InstanceDir)
	if err != nil {
		return Outcome{}, err
	}
	if bl == nil { // the loop writes it on first sight (FirstSight); without one only covered paths are known
		bl = &Baseline{Branch: t.Branch, Digests: map[string]string{}}
	}
	names, all, serr := lifecycle.StartedServices(t.InstanceDir, plan, mode)
	started := func(s string) bool { return serr != nil || all || slices.Contains(names, s) }
	job := BuildJob(plan, mode, t.Changes, started)
	job.Cause.Upstream = t.Upstream
	if hasCategory(t.Changes, CatPlan) {
		job.Cause.PlanChanged = bl.PlanChanged(plan)
	}
	out := Outcome{Notes: job.Notes}
	upTo := ""
	if t.Upstream != nil {
		upTo = t.Upstream.To
	}
	if job.Empty(plan) { // a plan edit, an excluded service, migration files without a schema step
		bl.SetPlan(plan)
		if err := bl.RecordRun(t.InstanceDir, TriggerLive, runs.Succeeded, job.Covered, upTo); err != nil {
			return out, err
		}
		out.Status = Settled
		return out, nil
	}

	// D9: skip, never queue.
	if runs.Busy(t.InstanceDir) {
		out.Status, out.Reason = Skipped, "a run holds the instance's run lock"
		return out, nil
	}
	if r.ActionLocked != nil && r.ActionLocked(t.Project) {
		out.Status, out.Reason = Skipped, "a dashboard action is running for this instance"
		return out, nil
	}
	now := r.now()
	r.mu.Lock()
	if r.last == nil {
		r.last, r.started = map[string]time.Time{}, map[string][]time.Time{}
	}
	switch {
	case r.busy:
		r.mu.Unlock()
		out.Status, out.Reason = Skipped, "another live run is in progress"
		return out, nil
	case !r.last[t.Project].IsZero() && now.Sub(r.last[t.Project]) < MinSpacing:
		r.mu.Unlock()
		out.Status, out.Reason = Skipped, "the last live run of this instance started less than 30 s ago"
		return out, nil
	case r.recent(t.Main, now) >= HourlyMax:
		delete(r.started, t.Main) // a resume starts a fresh hour
		r.mu.Unlock()
		return r.pause(t, Pause{Kind: PauseRate, Reason: fmt.Sprintf("rate limit: %d live runs in the last hour", HourlyMax)})
	}
	r.busy = true
	r.mu.Unlock()

	res, execErr := r.Exec(ctx, t, job)
	r.mu.Lock()
	r.busy = false
	if res.ID != 0 {
		r.last[t.Project] = now
		r.started[t.Main] = append(r.started[t.Main], now)
	}
	r.mu.Unlock()
	out.Run = res

	switch {
	case res.ID == 0 && errors.Is(execErr, runs.ErrBusy):
		out.Status, out.Reason = Skipped, "a run holds the instance's run lock"
		return out, nil
	case res.ID == 0 && execErr != nil: // refused before a run: the same refusal on every tick would loop
		p, err := r.pause(t, Pause{Kind: PauseRefused, Reason: "could not start a live run: " + execErr.Error()})
		p.Notes = out.Notes
		return p, err
	case res.Outcome == runs.Succeeded:
		bl.SetPlan(plan)
		out.Status = Ran
		return out, bl.RecordRun(t.InstanceDir, TriggerLive, res.Outcome, job.Covered, upTo)
	}
	p, err := r.pause(t, Pause{Kind: PauseFailed, Reason: failedReason(res.ID, res.Outcome, res.Step), Run: res.ID, Step: res.Step})
	p.Status, p.Run, p.Notes = Ran, res, out.Notes
	return p, err
}

// recent counts the live runs of a repository in the last hour and drops the older ones.
func (r *Reconciler) recent(main string, now time.Time) int {
	l := r.started[main]
	for len(l) > 0 && now.Sub(l[0]) >= time.Hour {
		l = l[1:]
	}
	r.started[main] = l
	return len(l)
}

// pause writes the pause file and returns the Paused outcome carrying it.
func (r *Reconciler) pause(t Target, p Pause) (Outcome, error) {
	p.At = r.now().UTC()
	if err := WritePause(t.NewBase, t.Main, p); err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: Paused, Reason: p.Reason, Pause: &p}, nil
}

func hasCategory(cs []Change, cat string) bool {
	return slices.ContainsFunc(cs, func(c Change) bool { return c.Category == cat })
}

// lastMode is the mode of the newest succeeded run of the instance (design
// D3): a person who changed the configured mode has not asked to move a
// running instance.
func lastMode(instanceDir string) string {
	list, _ := runs.List(instanceDir)
	for _, r := range list {
		if r.Outcome == runs.Succeeded && r.Mode != "" {
			return r.Mode
		}
	}
	return ""
}

// Targets lists the instances of a repository: one per checkout of main
// (repos.Checkouts) that has an instance directory with a manifest, with the
// inventory state. Stopped ones are listed too; Reconcile leaves them alone
// and their changes stay pending.
func Targets(newBase, legacyBase, main, version string) ([]Target, error) {
	inv, err := inventory.Collect(inventory.Options{NewStateBase: newBase, LegacyStateBase: legacyBase, Version: version})
	if err != nil {
		return nil, err
	}
	roots := repos.Checkouts(main)
	var out []Target
	for _, in := range inv.Instances {
		if in.Legacy || !slices.ContainsFunc(roots, func(r string) bool { return sameFile(r, in.Root) }) {
			continue
		}
		out = append(out, Target{Main: main, Root: in.Root, Project: in.Project, InstanceDir: filepath.Join(newBase, in.Project),
			Branch: in.Branch, State: in.State, NewBase: newBase, LegacyBase: legacyBase})
	}
	return out, nil
}

func sameFile(a, b string) bool {
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && ra == rb
}
