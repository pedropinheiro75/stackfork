package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// StartHostApps is the host half of `up` for a hybrid instance: every host app
// the instance recorded and that is not running starts again through the
// `host start` path (HostRestart, from its recorded command), whatever the
// plan's mode says (an instance with host apps is hybrid in practice), and in
// hybrid mode so does every host step of the plan that has no record yet
// (HostStart). Each app is
// a `host` step of rec (nil: nothing is recorded) with its own state.
//
// An untrusted plan that has host steps starts nothing: a host command runs
// only on a machine that approved the plan. Call it with the instance lock
// released, because the supervisor takes that lock.
func StartHostApps(rec *runs.Run, out io.Writer, base []string, root, main, instanceDir, newBase, legacyBase, mode string, plan knowledge.Plan) error {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	type app struct {
		name string
		step *knowledge.Step // the plan's host step, when it has one
	}
	var apps []app
	recorded := map[string]bool{}
	if m != nil {
		for n := range m.HostProcs {
			recorded[n] = true
			apps = append(apps, app{name: n})
		}
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].name < apps[j].name })
	run, _ := plan.ForMode(mode)
	for i, s := range run {
		if mode == knowledge.ModeHybrid && s.Kind == knowledge.KindHost && !recorded[s.Name] {
			apps = append(apps, app{name: s.Name, step: &run[i]})
		}
	}
	if len(apps) == 0 {
		return nil
	}
	untrusted := false
	if ok, _ := TrustedHere(newBase, main, plan, mode); !ok {
		untrusted = true
		fmt.Fprintln(out, "host apps not started: the plan has host steps nobody approved on this machine; run: stackfork plan --trust")
	}
	var errs []error
	for _, a := range apps {
		id := "host-" + a.name
		if a.step != nil {
			id = a.step.ID
		}
		var step *runs.Step
		var w io.Writer = out
		skip := func(reason string) {
			fmt.Fprintf(out, "host app %s: %s\n", a.name, reason)
			if rec != nil {
				rec.Step(id, knowledge.KindHost, "host app "+a.name).Skip(reason)
			}
		}
		begin := func() {
			if rec != nil {
				step = rec.Step(id, knowledge.KindHost, "host app "+a.name)
				w = step.Start()
			}
		}
		switch {
		case untrusted:
			skip("the plan is not trusted here")
			continue
		case recorded[a.name]:
			proc := m.HostProcs[a.name]
			switch {
			case state.HostIdentity(proc):
				skip("already running")
				continue
			case proc.RestartArgs() == nil:
				skip("was not started with `host start`; start it again")
				continue
			}
			begin()
			fmt.Fprintf(w, "starting host app %s\n", a.name)
			err := HostRestart(w, root, instanceDir, a.name, newBase, legacyBase)
			if step != nil {
				step.End(err, 0)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("host app %s: %w", a.name, err))
			}
		default:
			begin()
			fmt.Fprintf(w, "starting host app %s: %s\n", a.name, a.step.Cmd)
			err := HostStart(w, w, base, root, instanceDir, *a.step)
			if step != nil {
				step.End(err, 0)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("host app %s: %w", a.name, err))
			}
		}
	}
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return errors.New(strings.Join(msgs, "; "))
	}
	return nil
}
