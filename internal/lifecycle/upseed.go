package lifecycle

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// UpSeed is what UpAndSeed needs: one Compose up and, when it creates the
// data, the one-time seed (design.md D9).
type UpSeed struct {
	Secrets                                                             SecretValues
	Root, Main, Branch, Project, InstanceDir, NewBase, LegacyBase, Mode string
	Plan                                                                knowledge.Plan
	Run                                                                 *runs.Run // nil: nothing is recorded
	UpID, UpText                                                        string    // the compose-up entry; "up" when empty
	// Compose runs Compose. With a run both writers are the step's writer.
	Compose func(stdout, stderr io.Writer) (code int, err error)
	Seed    bool // false: Compose only (the `up` a Start runs inside its own run)
	Lock    bool // take the instance lock around the seed decision and the seed (the caller does not hold it)
	// FreshData: the data was just deleted (a reset), so the seed runs whenever
	// there is a seed command, even when the volume check cannot tell.
	FreshData bool
	// DeferSeed: decide the seed at the up (that is where the volume is
	// created) but leave running it to the caller (RunDueSeed), at the data
	// step's place in the plan.
	DeferSeed bool
	// RetryFailed: a seed that failed on this data (seeded:false, not pending)
	// is due again. Only the plan runner sets it: it runs the seed after the
	// steps the seed needs.
	RetryFailed bool
}

// UpOutcome is the result of UpAndSeed. A failed seed is SeedErr: Code and Err
// stay what Compose returned, as `up` has always done.
type UpOutcome struct {
	Code    int
	Err     error
	SeedErr error
	Seeded  bool // this call ran the seed and it succeeded
	SeedDue bool // DeferSeed: the seed must run at its place
}

// SeedEntry is the plan's `exec once: data` step for mode or, when the seed is
// auto-detected, a synthetic step with id "seed". Its Cmd is the command that
// really runs (SeedRecipeIn: with the migrate prefix). ok is false when the
// mode has no seed at all.
func SeedEntry(root, main, instanceDir string, plan knowledge.Plan, mode string) (step knowledge.Step, ok bool) {
	cmd, svc := SeedRecipeIn(root, main, instanceDir, mode)
	run, _ := plan.ForMode(mode)
	for _, s := range run {
		if s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData {
			if cmd != "" {
				s.Cmd = cmd
			}
			return s, true
		}
	}
	if cmd == "" {
		return knowledge.Step{}, false
	}
	e := knowledge.Step{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: cmd, Service: svc}
	if _, _, host := seedRecipeIn(root, main, instanceDir, mode); host { // a live run's plan has no data step, the checkout's does
		e.On = knowledge.OnHost
	}
	return e, true
}

// RunOrder is the order a Start runs and shows steps in. The data step runs
// right after the first compose-up (design.md D9), or last when the plan has
// none, unless exec steps sit between the compose-up and the recorded data
// step (install, build, migrate): then it stays at its recorded place, since
// it needs what they prepare. seed is SeedEntry's result.
func RunOrder(steps []knowledge.Step, seed knowledge.Step, hasSeed bool) []knowledge.Step {
	out := make([]knowledge.Step, 0, len(steps)+1)
	if hasSeed && seedDeferredIn(steps) {
		for _, s := range steps {
			if s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData {
				s = seed
			}
			out = append(out, s)
		}
		return out
	}
	for _, s := range steps {
		if s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData {
			continue
		}
		out = append(out, s)
		if s.Kind == knowledge.KindComposeUp && hasSeed {
			out, hasSeed = append(out, seed), false
		}
	}
	if hasSeed {
		out = append(out, seed)
	}
	return out
}

// seedDeferredIn: steps has a data step after the first compose-up with an
// exec step between them, so the seed runs at that place, not with the up.
func seedDeferredIn(steps []knowledge.Step) bool {
	up, between := false, false
	for _, s := range steps {
		switch {
		case s.Kind == knowledge.KindComposeUp:
			up = true
		case s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData:
			return up && between
		case up && s.Kind == knowledge.KindExec:
			between = true
		}
	}
	return false
}

// SeedDeferred reports whether a Start of plan in mode runs the seed at its
// recorded place, after the exec steps that follow the compose-up.
func SeedDeferred(plan knowledge.Plan, mode string) bool {
	run, _ := plan.ForMode(mode)
	return seedDeferredIn(run)
}

func withLock(do bool, instanceDir string, fn func()) {
	if !do {
		fn()
		return
	}
	if l, err := state.AcquireLock(filepath.Join(instanceDir, ".lock")); err == nil {
		defer l.Release()
	}
	fn()
}

// UpAndSeed decides the seed, runs Compose as the compose-up entry and, when
// this up creates the data, runs the seed as its own entry. `up`, the card's
// Start and the plan runner all call it.
func UpAndSeed(out, errOut io.Writer, a UpSeed) UpOutcome {
	var seedStep knowledge.Step
	var hasSeed, seedNow bool
	if a.Seed {
		seedStep, hasSeed = SeedEntry(a.Root, a.Main, a.InstanceDir, a.Plan, a.Mode)
		withLock(a.Lock, a.InstanceDir, func() {
			if a.FreshData {
				cmd, _ := SeedRecipeIn(a.Root, a.Main, a.InstanceDir, a.Mode)
				if seedNow = cmd != ""; seedNow {
					setSeed(a.InstanceDir, boolPtr(false), true)
				}
				return
			}
			seedNow = planSeed(a.Root, a.Main, a.InstanceDir, a.Mode, a.RetryFailed)
		})
	}
	var upS, seedS *runs.Step
	if a.Run != nil {
		id, text := a.UpID, a.UpText
		if id == "" {
			id = "up"
		}
		if text == "" {
			text = "compose up"
		}
		upS = a.Run.Step(id, knowledge.KindComposeUp, text)
		if hasSeed {
			seedS = a.Run.Step(seedStep.ID, knowledge.KindExec, StepText(seedStep))
		}
	}
	so, se := out, errOut
	if upS != nil {
		w := upS.Start()
		so, se = w, w
	}
	code, err := a.Compose(so, se)
	res := UpOutcome{Code: code, Err: err}
	failed := err
	if failed == nil && code != 0 {
		failed = fmt.Errorf("exited with status %d", code)
	}
	if upS != nil {
		upS.End(failed, code)
	}
	if failed != nil || !hasSeed {
		return res
	}
	if a.DeferSeed {
		res.SeedDue = seedNow
		return res
	}
	if !seedNow {
		if seedS != nil && seedS.State == runs.Pending {
			reason := "data already exists"
			if cmd, _ := SeedRecipeIn(a.Root, a.Main, a.InstanceDir, a.Mode); cmd == "" {
				reason = "no seed for this mode"
			}
			seedS.Skip(reason)
		}
		return res
	}
	if seedNeedsLaterSteps(a) {
		// The plan has install/build/migrate steps before the seed: a plain up
		// would seed before they ran. The seed stays pending for start-env.
		const note = "seed pending: it runs with start-env, after the plan's install/build/migrate steps (stackfork start-env)"
		fmt.Fprintln(errOut, note)
		if seedS != nil {
			seedS.Skip(note)
		}
		return res
	}
	res.SeedErr, res.Seeded = runDueSeed(out, errOut, a, seedS)
	return res
}

// RunError is the error the run of an `up` ends with. It also sets the run's
// retry command: a failed seed is retried by `seed`, anything else by `up`.
func (o UpOutcome) RunError(rec *runs.Run, project string) error {
	cmd := "stackfork --instance " + project + " "
	switch {
	case o.Err != nil:
		rec.SetRetry(cmd + "up")
		return o.Err
	case o.Code != 0:
		rec.SetRetry(cmd + "up")
		return fmt.Errorf("up exited with status %d", o.Code)
	case o.SeedErr != nil:
		rec.SetRetry(cmd + "seed")
		return fmt.Errorf("seed failed: %v; retry with stackfork seed", o.SeedErr)
	}
	return nil
}

// RunDueSeed runs the seed UpAndSeed decided with DeferSeed, at the data step's
// place. seedS is the step's run entry (nil: not recorded).
func RunDueSeed(out, errOut io.Writer, a UpSeed, seedS *runs.Step) (seedErr error, seeded bool) {
	return runDueSeed(out, errOut, a, seedS)
}

func runDueSeed(out, errOut io.Writer, a UpSeed, seedS *runs.Step) (seedErr error, seeded bool) {
	skipped := false
	withLock(a.Lock, a.InstanceDir, func() {
		cmd, svc := SeedRecipeIn(a.Root, a.Main, a.InstanceDir, a.Mode)
		if note := DraftSeedSkip(a.Root, a.Main, a.InstanceDir, a.Mode); note != "" && cmd != "" {
			// the seed stays pending: it runs once the person records it
			fmt.Fprintln(errOut, note)
			if seedS != nil {
				seedS.Skip(note)
			}
			skipped = true
			return
		}
		if _, _, host := seedRecipeIn(a.Root, a.Main, a.InstanceDir, a.Mode); cmd != "" && svc == "" && !host &&
			ResolveExecService(a.InstanceDir, knowledge.Step{}, a.Plan) == "" && seedAutoDetected(a.Root, a.Main, a.InstanceDir, a.Mode) {
			// the stack is up and the script was only guessed: the person decides where it runs
			note := fmt.Sprintf("seed not run: no service can run `%s`; record it with `stackfork learn record step --id seed --kind exec --once data --service SERVICE --cmd %q` (or --on host)", cmd, cmd)
			fmt.Fprintln(errOut, note)
			if seedS != nil {
				seedS.Skip(note)
			}
			skipped = true
			return
		}
		seedErr = RunSeed(out, errOut, a.Root, a.Main, a.Branch, a.Project, a.InstanceDir, a.NewBase, a.LegacyBase, cmd, svc, a.Mode, a.Plan, seedS, a.Secrets)
	})
	return seedErr, seedErr == nil && !skipped
}

// seedNeedsLaterSteps: the plan runs an exec step between the compose-up and
// the data step that the seed does not cover itself (install, build, a
// migrate the seed command does not run), so a seed right after the up fails.
func seedNeedsLaterSteps(a UpSeed) bool {
	if !SeedDeferred(a.Plan, a.Mode) {
		return false
	}
	run, _ := a.Plan.ForMode(a.Mode)
	up := false
	for _, s := range run {
		switch {
		case s.Kind == knowledge.KindComposeUp:
			up = true
		case s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData:
			return false
		case up && s.Kind == knowledge.KindExec && !(s.Once == knowledge.OnceSchema && SeedCoversSchema(a.Root, a.Main, a.InstanceDir, a.Plan, a.Mode, s)):
			return true
		}
	}
	return false
}
