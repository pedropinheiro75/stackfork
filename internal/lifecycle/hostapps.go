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

// StartHostApps is the host half of `up`. In hybrid mode:
//   - a host step of the plan that is not running starts through `host start`
//     (HostStart), with the plan's command even when an older run recorded
//     another, unless a person stopped that app;
//   - a recorded app that is not in the plan starts again from its recorded
//     command (HostRestart) only if it was a server that ended by itself (see
//     restartable): a one-shot command, a clean exit and an app a person
//     stopped are left alone.
//
// Each app is a `host` step of rec (nil: nothing is recorded) with its own
// state. An untrusted plan that has host steps starts nothing: a host command
// runs only on a machine that approved the plan. Call it with the instance
// lock released, because the supervisor takes that lock.
func StartHostApps(rec *runs.Run, out io.Writer, base []string, root, main, instanceDir, newBase, legacyBase, mode string, plan knowledge.Plan) error {
	hybrid := mode == knowledge.ModeHybrid
	// Outside hybrid mode a recorded app still comes back, unless the stack
	// runs a container of the same name (it would run twice).
	inStack := map[string]bool{}
	if !hybrid {
		for _, n := range SnapshotServices(instanceDir) {
			inStack[n] = true
		}
	}
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	type app struct {
		name string
		step *knowledge.Step // the plan's host step, when it has one
	}
	var apps []app
	run, _ := plan.ForMode(mode)
	planned := map[string]*knowledge.Step{}
	for i, s := range run {
		if s.Kind == knowledge.KindHost {
			planned[s.Name] = &run[i]
		}
	}
	recorded := map[string]bool{}
	if m != nil {
		for n := range m.HostProcs {
			recorded[n] = true
			if inStack[n] {
				continue
			}
			apps = append(apps, app{name: n, step: planned[n]})
		}
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].name < apps[j].name })
	for i, s := range run {
		if hybrid && s.Kind == knowledge.KindHost && !recorded[s.Name] {
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
		case recorded[a.name] && a.step == nil:
			proc := m.HostProcs[a.name]
			switch {
			case state.HostIdentity(proc):
				skip("already running")
				continue
			case proc.RestartArgs() == nil:
				skip("was not started with `host start`; start it again")
				continue
			}
			if why := notRestartable(instanceDir, a.name); why != "" {
				skip(why)
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
			if recorded[a.name] {
				if state.HostIdentity(m.HostProcs[a.name]) {
					skip("already running")
					continue
				}
				if r, _ := state.ReadHostRecord(instanceDir, a.name); r != nil && r.StoppedByPerson {
					skip("stopped by a person; start it with `stackfork host restart " + a.name + "`")
					continue
				}
			}
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

// notRestartable says why `up` leaves a recorded host app down, or "" when it
// should start it again: it must have listened on its port at least once (a
// server, not a one-shot), have ended without a person stopping it, and not
// have exited cleanly with code 0.
func notRestartable(instanceDir, name string) string {
	r, _ := state.ReadHostRecord(instanceDir, name)
	switch {
	case r == nil:
		return "not restarted: nothing is recorded about how it ended"
	case r.StoppedByPerson:
		return "stopped by a person; start it with `stackfork host restart " + name + "`"
	case !r.Listened:
		return "not restarted: it never listened on its port (a one-shot command?)"
	case r.ExitCode != nil && *r.ExitCode == 0:
		return "not restarted: it exited cleanly (status 0)"
	}
	return ""
}
