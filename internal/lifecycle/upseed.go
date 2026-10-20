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
	Root, Main, Branch, Project, InstanceDir, NewBase, LegacyBase, Mode string
	Plan                                                                knowledge.Plan
	Run                                                                 *runs.Run // nil: nothing is recorded
	UpID, UpText                                                        string    // the compose-up entry; "up" when empty
	// Compose runs Compose. With a run both writers are the step's writer.
	Compose func(stdout, stderr io.Writer) (code int, err error)
	Seed    bool // false: Compose only (the `up` a Start runs inside its own run)
	Lock    bool // take the instance lock around the seed decision and the seed (the caller does not hold it)
}

// UpOutcome is the result of UpAndSeed. A failed seed is SeedErr: Code and Err
// stay what Compose returned, as `up` has always done.
type UpOutcome struct {
	Code    int
	Err     error
	SeedErr error
	Seeded  bool // this call ran the seed and it succeeded
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
	return knowledge.Step{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: cmd, Service: svc}, true
}

// RunOrder is the order a Start runs and shows steps in: the data step is
// not at its place in the plan but right after the first compose-up (design.md
// D9), or last when the plan has none. seed is SeedEntry's result.
func RunOrder(steps []knowledge.Step, seed knowledge.Step, hasSeed bool) []knowledge.Step {
	out := make([]knowledge.Step, 0, len(steps)+1)
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
		withLock(a.Lock, a.InstanceDir, func() { seedNow = PlanSeedIn(a.Root, a.Main, a.InstanceDir, a.Mode) })
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
	withLock(a.Lock, a.InstanceDir, func() {
		cmd, svc := SeedRecipeIn(a.Root, a.Main, a.InstanceDir, a.Mode)
		res.SeedErr = RunSeed(out, errOut, a.Root, a.Branch, a.Project, a.InstanceDir, a.NewBase, a.LegacyBase, cmd, svc, a.Plan, seedS)
	})
	res.Seeded = res.SeedErr == nil
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
