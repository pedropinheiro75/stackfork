package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// HostStart starts one host step. The cli package sets the real one (`host start`); tests stub it.
// base is the global flags (state dirs, --root) of the start that runs the step.
var HostStart = func(out, errOut io.Writer, base []string, root, instanceDir string, step knowledge.Step) error {
	return errors.New("host steps are not implemented yet")
}

// HostExec runs one exec step with on: host to completion and returns the
// command's exit status. The cli package sets the real one; tests stub it.
// Its output goes to w, the step writer (redacted); stdin is closed.
var HostExec = func(w io.Writer, project, root, instanceDir, newBase, legacyBase string, step knowledge.Step) (int, error) {
	return 0, errors.New("host exec steps are not implemented yet")
}

// ErrAlreadyPrepared is what a Prepare function may return to say it had
// nothing to do; the runner then records the step as skipped.
var ErrAlreadyPrepared = errors.New("already prepared")

// PlanRunner runs the startup plan of one checkout in one mode. The funcs
// write to w, the writer of the step that calls them.
type PlanRunner struct {
	Out, Err                                                            io.Writer
	Root, Main, Branch, Project, InstanceDir, NewBase, LegacyBase, Mode string
	Plan                                                                knowledge.Plan
	Run                                                                 *runs.Run                                  // nil: nothing is recorded
	Prepared                                                            func() bool                                // true when a prepare step has nothing to do
	Prepare                                                             func(w io.Writer, args []string) error     // the CLI's prepare path; decides itself whether it is needed
	Up                                                                  func(w io.Writer, services []string) error // the CLI's `up --in-run` path
	Host                                                                func(w io.Writer, step knowledge.Step) error
	FreshData                                                           bool // a reset deleted the data: the seed runs whatever the volume check says
	// Exact runs Plan's steps for the mode as they are: no check, no implicit
	// prepare, no implicit compose-up. A live run sets it with a plan it cut
	// from the full plan; its caller checked and trusted the full one.
	Exact bool
}

// Check refuses an untrusted host step and an unsupported mode; it touches nothing.
func (r PlanRunner) Check() error {
	if ok, _ := TrustedHere(r.NewBase, r.Main, r.Plan, r.Mode); !ok {
		return errUntrusted(r.NewBase, r.Main, r.Plan)
	}
	if !r.Plan.SupportsMode(r.Mode) {
		sup := []string{}
		for _, m := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
			if r.Plan.SupportsMode(m) {
				sup = append(sup, m)
			}
		}
		return fmt.Errorf("mode %q is not supported by this plan; supported: %s", r.Mode, strings.Join(sup, ", "))
	}
	run, _ := r.Plan.ForMode(r.Mode)
	excluded := excludedServices(run)
	for _, st := range run {
		if st.Kind != knowledge.KindExec || st.IsHostExec() { // a host step has no service to exclude
			continue
		}
		svc := ResolveExecService(r.InstanceDir, st, r.Plan)
		if svc != "" && contains(excluded, svc) {
			return fmt.Errorf("step %s runs in %s, which this mode does not start; set --service on the step", st.ID, svc)
		}
	}
	return nil
}

// Execute checks, then runs the steps in list order, no retries.
func (r PlanRunner) Execute() error {
	steps, _ := r.Plan.ForMode(r.Mode)
	if !r.Exact {
		if err := r.Check(); err != nil {
			return err
		}
		hasUp := false
		for _, s := range steps {
			hasUp = hasUp || s.Kind == knowledge.KindComposeUp
		}
		if !hasUp { // a plan without compose-up (e.g. none recorded) still starts the stack
			steps = append(steps, knowledge.Step{ID: "up", Kind: knowledge.KindComposeUp})
		}
		steps = withImplicitPrepare(steps)
	}
	r.declare(steps)
	prepared, seeded := false, false
	for _, s := range steps {
		var err error
		switch s.Kind {
		case knowledge.KindPrepare:
			switch {
			case prepared || (r.Prepared != nil && r.Prepared()):
				prepared = true
				fmt.Fprintf(r.Out, "%s is already prepared\n", r.Project)
				r.skip(s, "already prepared")
			default:
				prepared = true
				err = r.step(s, func(w io.Writer) (int, error) { return 0, r.prepare(w, s.Args) })
			}
		case knowledge.KindComposeUp:
			var sd bool
			sd, err = r.composeUp(s)
			seeded = seeded || sd
		case knowledge.KindExec:
			switch {
			case s.Once == knowledge.OnceData: // the data seed runs with its compose-up
			case s.Once == knowledge.OnceSchema && seeded && SeedCoversSchema(r.Root, r.Main, r.InstanceDir, r.Plan, r.Mode, s): // the seed prefix (migrate && seed) already ran it
				r.skip(s, "ran with the seed")
			default:
				err = r.step(s, func(w io.Writer) (int, error) { return r.exec(w, s) })
			}
		case knowledge.KindHost:
			err = r.step(s, func(w io.Writer) (int, error) { return 0, r.Host(w, s) })
		case knowledge.KindNote:
			err = r.step(s, func(w io.Writer) (int, error) { fmt.Fprintf(w, "note: %s\n", s.Text); return 0, nil })
		}
		if err != nil {
			var se stepError
			if errors.As(err, &se) || strings.HasPrefix(err.Error(), "step "+s.ID+":") {
				return err
			}
			return fmt.Errorf("step %s failed: %v; retry with stackfork start-env", s.ID, err)
		}
	}
	return nil
}

// withImplicitPrepare puts a prepare step before the first compose-up unless
// a prepare step already comes first: an up always needs a prepared instance,
// so that work is a step of the run too (logged, redacted, able to fail).
func withImplicitPrepare(steps []knowledge.Step) []knowledge.Step {
	id := "prepare"
	for _, s := range steps {
		if s.ID == id { // a later prepare step of the plan keeps its id
			id = "implicit-prepare"
		}
	}
	for i, s := range steps {
		switch s.Kind {
		case knowledge.KindPrepare:
			return steps
		case knowledge.KindComposeUp:
			out := append([]knowledge.Step{}, steps[:i]...)
			out = append(out, knowledge.Step{ID: id, Kind: knowledge.KindPrepare})
			return append(out, steps[i:]...)
		}
	}
	return steps
}

// prepare runs the Prepare func; "already prepared" is not an error.
func (r PlanRunner) prepare(w io.Writer, args []string) error {
	if err := r.Prepare(w, args); err != nil && !errors.Is(err, ErrAlreadyPrepared) {
		return err
	}
	return nil
}

// declare lists every step of the run as pending. The seed entry follows the
// first compose-up (design.md D9), so the plan's own data step is not listed
// at its place in the plan.
func (r PlanRunner) declare(steps []knowledge.Step) {
	if r.Run == nil {
		return
	}
	seed, hasSeed := SeedEntry(r.Root, r.Main, r.InstanceDir, r.Plan, r.Mode)
	for _, s := range RunOrder(steps, seed, hasSeed) {
		r.Run.Step(s.ID, s.Kind, StepText(s))
	}
}

// step runs fn as the step s: its writer is the step's writer, its result the step's.
func (r PlanRunner) step(s knowledge.Step, fn func(w io.Writer) (int, error)) error {
	if r.Run == nil {
		_, err := fn(r.Out)
		return err
	}
	rs := r.Run.Step(s.ID, s.Kind, StepText(s))
	code, err := fn(rs.Start())
	rs.End(err, code)
	return err
}

func (r PlanRunner) skip(s knowledge.Step, reason string) {
	if r.Run != nil {
		r.Run.Step(s.ID, s.Kind, StepText(s)).Skip(reason)
	}
}

// composeUp runs Up as the compose-up step and, when that up creates the
// data, the seed (UpAndSeed), and reports whether the seed ran. The inner up
// runs with --in-run: it neither records nor seeds, this call does both.
func (r PlanRunner) composeUp(s knowledge.Step) (seeded bool, err error) {
	res := UpAndSeed(r.Out, r.Err, UpSeed{
		Run: r.Run, Root: r.Root, Main: r.Main, Branch: r.Branch, Project: r.Project, InstanceDir: r.InstanceDir,
		NewBase: r.NewBase, LegacyBase: r.LegacyBase, Mode: r.Mode, Plan: r.Plan,
		UpID: s.ID, UpText: StepText(s), Seed: true, Lock: true, FreshData: r.FreshData,
		Compose: func(stdout, stderr io.Writer) (int, error) {
			services, err := r.upServices(stdout, s)
			if err != nil {
				return 0, err
			}
			return 0, r.Up(stdout, services)
		},
	})
	if res.Err != nil {
		return false, res.Err
	}
	if res.SeedErr != nil {
		if r.Run != nil {
			r.Run.SetRetry("stackfork --instance " + r.Project + " seed")
		}
		return false, seedFailure(r.Plan, r.Root, r.Main, r.InstanceDir, r.Mode, res.SeedErr)
	}
	return res.Seeded, nil
}

// stepError is an error that already names its step and its retry.
type stepError struct{ error }

// seedFailure is the error of a failed seed step; the retry is `seed`.
func seedFailure(plan knowledge.Plan, root, main, instanceDir, mode string, err error) error {
	id := "seed"
	if st, ok := SeedEntry(root, main, instanceDir, plan, mode); ok {
		id = st.ID
	}
	return stepError{fmt.Errorf("step %s failed: %v; retry with stackfork seed", id, err)}
}

// upServices is the service list of a compose-up step: nil for all, else all
// but the excluded ones and whatever depends on them.
func (r PlanRunner) upServices(w io.Writer, s knowledge.Step) ([]string, error) {
	var services []string
	if len(s.Exclude) > 0 {
		data, err := os.ReadFile(filepath.Join(r.InstanceDir, "compose.json"))
		if err != nil {
			return nil, err
		}
		model, err := compose.DecodeJSON(data)
		if err != nil {
			return nil, err
		}
		top, _ := model.(map[string]interface{})
		svcs, _ := top["services"].(map[string]interface{})
		// drop the excluded services and, transitively, whatever depends on them:
		// compose would start an excluded service again for its dependent.
		drop := map[string]bool{}
		for _, x := range s.Exclude {
			drop[x] = true
		}
		for changed := true; changed; {
			changed = false
			for name, v := range svcs {
				if drop[name] {
					continue
				}
				sv, _ := v.(map[string]interface{})
				for _, d := range dependsOn(sv["depends_on"]) {
					if drop[d] {
						drop[name], changed = true, true
						break
					}
				}
			}
		}
		var dependents []string
		for name := range svcs {
			if !drop[name] {
				services = append(services, name)
			} else if !contains(s.Exclude, name) {
				dependents = append(dependents, name)
			}
		}
		sort.Strings(services)
		sort.Strings(dependents)
		if len(services) == 0 {
			return nil, fmt.Errorf("step %s: exclude leaves no services to start", s.ID)
		}
		if len(dependents) > 0 {
			fmt.Fprintf(w, "hybrid: not starting %s (they depend on %s)\n", strings.Join(dependents, ", "), strings.Join(s.Exclude, ", "))
		}
	}
	return services, nil
}

// exec runs an exec step in its service, or on the host for on: host, and
// returns the command's exit status.
func (r PlanRunner) exec(w io.Writer, s knowledge.Step) (int, error) {
	if s.IsHostExec() {
		fmt.Fprintf(w, "running %s on the host: %s\n", s.ID, s.Cmd)
		code, err := HostExec(w, r.Project, r.Root, r.InstanceDir, r.NewBase, r.LegacyBase, s)
		if err == nil && code != 0 {
			err = fmt.Errorf("exited with status %d", code)
		}
		return code, err
	}
	svc := ResolveExecService(r.InstanceDir, s, r.Plan)
	if have := SnapshotServices(r.InstanceDir); svc == "" || !contains(have, svc) {
		which := "it names no service"
		if svc != "" {
			which = "its service " + svc + " is not in this stack"
		}
		if h := hostHint(r.InstanceDir, r.Plan, s.ID, s.Cmd, s.Once); h != "" {
			return 0, fmt.Errorf("step %s cannot run: %s (this stack has: %s); %s", s.ID, which, strings.Join(have, ", "), h)
		}
		return 0, fmt.Errorf("step %s cannot run: %s (this stack has: %s). Record the service that can run `%s` with: stackfork learn record step --id %s --kind exec --service SERVICE --cmd %q",
			s.ID, which, strings.Join(have, ", "), s.Cmd, s.ID, s.Cmd)
	}
	fmt.Fprintf(w, "running %s: %s\n", s.ID, s.Cmd)
	code, err := Execute(w, w, "exec", r.Root, r.Branch, r.Project, r.InstanceDir, r.NewBase, r.LegacyBase,
		Options{Service: svc, Command: []string{"sh", "-c", s.Cmd}})
	if err != nil {
		return 0, err
	}
	if code != 0 {
		return code, fmt.Errorf("exited with status %d", code)
	}
	return 0, nil
}

// StartedServices are the services the mode's compose-up steps start. all is
// true when one of them starts every service (no exclude), or the plan has no
// compose-up step, which starts the whole stack.
func StartedServices(instanceDir string, plan knowledge.Plan, mode string) (names []string, all bool, err error) {
	run, _ := plan.ForMode(mode)
	r := PlanRunner{InstanceDir: instanceDir}
	seen := false
	for _, s := range run {
		if s.Kind != knowledge.KindComposeUp {
			continue
		}
		seen = true
		list, err := r.upServices(io.Discard, s)
		if err != nil {
			return nil, false, err
		}
		if list == nil {
			return nil, true, nil
		}
		names = append(names, list...)
	}
	return names, !seen, nil
}

// excludedServices are the services the steps' compose-up leave out.
func excludedServices(run []knowledge.Step) []string {
	var excluded []string
	for _, st := range run {
		if st.Kind == knowledge.KindComposeUp {
			excluded = append(excluded, st.Exclude...)
		}
	}
	return excluded
}

func contains(l []string, x string) bool {
	for _, v := range l {
		if v == x {
			return true
		}
	}
	return false
}

// dependsOn reads depends_on in its list and map forms.
func dependsOn(v interface{}) []string {
	var out []string
	switch d := v.(type) {
	case []interface{}:
		for _, x := range d {
			if n, ok := x.(string); ok {
				out = append(out, n)
			}
		}
	case map[string]interface{}:
		for n := range d {
			out = append(out, n)
		}
	}
	return out
}
