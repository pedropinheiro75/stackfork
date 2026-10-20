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
)

// HostStart starts one host step. The cli package sets the real one (`host start`); tests stub it.
// base is the global flags (state dirs, --root) of the start that runs the step.
var HostStart = func(out, errOut io.Writer, base []string, root, instanceDir string, step knowledge.Step) error {
	return errors.New("host steps are not implemented yet")
}

// PlanRunner runs the startup plan of one checkout in one mode.
type PlanRunner struct {
	Out, Err                                                            io.Writer
	Root, Main, Branch, Project, InstanceDir, NewBase, LegacyBase, Mode string
	Plan                                                                knowledge.Plan
	Prepare                                                             func(args []string) error     // the CLI's prepare path; decides itself whether it is needed
	Up                                                                  func(services []string) error // the CLI's `up` path (keeps PlanSeed/RunSeed inside up)
	Host                                                                func(step knowledge.Step) error
}

// Check refuses an untrusted host step and an unsupported mode; it touches nothing.
func (r PlanRunner) Check() error {
	if ok, _ := TrustedHere(r.NewBase, r.Main, r.Plan, r.Mode); !ok {
		diff := ReadableDiff(r.NewBase, r.Main, r.Plan)
		return fmt.Errorf("not trusted on this machine: the plan has host steps nobody approved here\n%s\nrun: stackfork plan --trust", strings.Join(diff, "\n"))
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
		if st.Kind != knowledge.KindExec {
			continue
		}
		svc := ResolveExecService(r.InstanceDir, st, r.Plan)
		if svc != "" && contains(excluded, svc) {
			return fmt.Errorf("step %s runs in %s, which this mode does not start; set --service on the step", st.ID, svc)
		}
	}
	return nil
}

// Run checks, then runs the steps in list order, no retries.
func (r PlanRunner) Run() error {
	if err := r.Check(); err != nil {
		return err
	}
	steps, _ := r.Plan.ForMode(r.Mode)
	hasUp := false
	for _, s := range steps {
		hasUp = hasUp || s.Kind == knowledge.KindComposeUp
	}
	if !hasUp { // a plan without compose-up (e.g. none recorded) still starts the stack
		steps = append(steps, knowledge.Step{ID: "up", Kind: knowledge.KindComposeUp})
	}
	prepared, seeded := false, false
	prepare := func(args []string) error {
		if prepared {
			return nil
		}
		prepared = true
		return r.Prepare(args)
	}
	for _, s := range steps {
		var err error
		switch s.Kind {
		case knowledge.KindPrepare:
			err = prepare(s.Args)
		case knowledge.KindComposeUp:
			if err = prepare(nil); err == nil {
				var sd bool
				sd, err = r.composeUp(s)
				seeded = seeded || sd
			}
		case knowledge.KindExec:
			if s.Once != knowledge.OnceData { // the data seed runs inside Up
				err = r.exec(s, seeded)
			}
		case knowledge.KindHost:
			err = r.Host(s)
		case knowledge.KindNote:
			fmt.Fprintf(r.Out, "note: %s\n", s.Text)
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

// composeUp runs Up as the compose-up step and, when that up creates the
// data, the seed (UpAndSeed), and reports whether the seed ran. The inner up
// runs with --in-run: it neither records nor seeds, this call does both.
func (r PlanRunner) composeUp(s knowledge.Step) (seeded bool, err error) {
	res := UpAndSeed(r.Out, r.Err, UpSeed{
		Root: r.Root, Main: r.Main, Branch: r.Branch, Project: r.Project, InstanceDir: r.InstanceDir,
		NewBase: r.NewBase, LegacyBase: r.LegacyBase, Mode: r.Mode, Plan: r.Plan,
		UpID: s.ID, UpText: StepText(s), Seed: true, Lock: true,
		Compose: func(stdout, stderr io.Writer) (int, error) {
			services, err := r.upServices(s)
			if err != nil {
				return 0, err
			}
			return 0, r.Up(services)
		},
	})
	if res.Err != nil {
		return false, res.Err
	}
	if res.SeedErr != nil {
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
func (r PlanRunner) upServices(s knowledge.Step) ([]string, error) {
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
			fmt.Fprintf(r.Out, "hybrid: not starting %s (they depend on %s)\n", strings.Join(dependents, ", "), strings.Join(s.Exclude, ", "))
		}
	}
	return services, nil
}

// exec runs a schema (or plain) exec step in its service.
func (r PlanRunner) exec(s knowledge.Step, seeded bool) error {
	if s.Once == knowledge.OnceSchema && seeded {
		return nil // the seed prefix (migrate && seed) already ran it
	}
	svc := ResolveExecService(r.InstanceDir, s, r.Plan)
	fmt.Fprintf(r.Out, "running %s: %s\n", s.ID, s.Cmd)
	code, err := Execute(r.Out, r.Err, "exec", r.Root, r.Branch, r.Project, r.InstanceDir, r.NewBase, r.LegacyBase,
		Options{Service: svc, Command: []string{"sh", "-c", s.Cmd}})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exited with status %d", code)
	}
	return nil
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
