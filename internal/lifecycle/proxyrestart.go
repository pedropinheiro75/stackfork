package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TriggerProxy is the run trigger of the restarts a proxy start or stop causes.
const TriggerProxy = "proxy"

// proxyTarget is a running host app whose command has a URL placeholder that
// the proxy changes.
type proxyTarget struct {
	Name string
	Step *knowledge.Step // the plan's host step; nil for a recorded app outside the plan
}

// proxyTargets selects the running host apps of an instance whose expanded
// command changes when the proxy starts or stops (knowledge.UsesProxyURL).
// running says whether a recorded process is alive.
func proxyTargets(m *state.Manifest, k *knowledge.Knowledge, plan knowledge.Plan, mode string, running func(state.HostProc) bool) []proxyTarget {
	entry := knowledge.ProxyEntryApp(k, m)
	if entry == "" {
		return nil
	}
	run, _ := plan.ForMode(mode)
	planned := map[string]*knowledge.Step{}
	for i, s := range run {
		if s.Kind == knowledge.KindHost {
			planned[s.Name] = &run[i]
		}
	}
	recorded := knowledge.HostApps(m)
	var out []proxyTarget
	for name, proc := range m.HostProcs {
		if !running(proc) {
			continue
		}
		var argv []string
		if s := planned[name]; s != nil {
			argv, _ = knowledge.SplitArgv(s.Cmd)
		} else {
			argv = recorded[name].Argv
		}
		if knowledge.UsesProxyURL(argv, name, entry) {
			out = append(out, proxyTarget{Name: name, Step: planned[name]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RestartHostAppsForProxy runs after the proxy started or stopped (what is
// "proxy up" or "proxy down"). It restarts, in every instance, the running
// host apps whose URL placeholders now expand differently, so their env
// matches the URL the person opens. Per instance it takes lockProject (nil:
// none) and a run (trigger "proxy"); an instance with a run in progress or
// a project another action holds is skipped, and so is one whose plan is not
// trusted here, which gets a warning on its card instead. The returned error
// joins what failed; the proxy change itself already happened.
func RestartHostAppsForProxy(out io.Writer, what, newBase, legacyBase string, lockProject func(project string) (unlock func(), ok bool)) error {
	var errs []string
	for _, b := range []struct{ base, prefix string }{{newBase, identity.Prefix}, {legacyBase, identity.LegacyPrefix}} {
		paths, _ := state.Manifests(b.base, b.prefix)
		for _, p := range paths {
			m, err := state.ReadManifest(p)
			if err != nil || m == nil || m.Root == "" || len(m.HostProcs) == 0 {
				continue
			}
			if err := restartForProxy(out, what, m, filepath.Dir(p), newBase, legacyBase, lockProject); err != nil {
				errs = append(errs, m.Project+": "+err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func restartForProxy(out io.Writer, what string, m *state.Manifest, instanceDir, newBase, legacyBase string, lockProject func(string) (func(), bool)) error {
	root := m.Root
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	main := identity.MainRepoOf(root)
	k, _ := knowledge.LoadForCheckout(root)
	plan, mode := LoadPlan(root, main), PlanMode(root, main)
	targets := proxyTargets(m, k, plan, mode, state.HostIdentity)
	if len(targets) == 0 {
		return nil
	}
	if lockProject != nil {
		unlock, ok := lockProject(m.Project)
		if !ok {
			fmt.Fprintf(out, "%s: an action is running; host apps keep their old URL until restarted\n", m.Project)
			return nil
		}
		defer unlock()
	}
	rec, err := runs.Begin(instanceDir, runs.Meta{Command: what, Trigger: TriggerProxy, Mode: mode, Project: m.Project, Branch: m.Branch, Out: out, NoWait: true})
	if err != nil {
		fmt.Fprintf(out, "%s: %s; host apps keep their old URL until restarted\n", m.Project, err)
		return nil
	}
	trusted, _ := TrustedHere(newBase, main, plan, mode)
	base := []string{"--root", root, "--state-dir", newBase, "--legacy-state-dir", legacyBase}
	var errs []error
	var stale []string
	for _, t := range targets {
		id := "host-" + t.Name
		if t.Step != nil {
			id = t.Step.ID
		}
		step := rec.Step(id, knowledge.KindHost, "restart host app "+t.Name)
		if t.Step != nil && !trusted {
			step.Skip("the plan is not trusted here; run: stackfork plan --trust")
			stale = append(stale, t.Name)
			continue
		}
		w := step.Start()
		if t.Step != nil {
			err = restartPlanStep(w, base, root, instanceDir, *t.Step)
		} else {
			err = HostRestart(w, root, instanceDir, t.Name, newBase, legacyBase)
		}
		step.End(err, 0)
		if err != nil {
			errs = append(errs, fmt.Errorf("host app %s: %w", t.Name, err))
		}
	}
	state.SetProxyStale(instanceDir, stale)
	err = errors.Join(errs...)
	rec.Finish(err)
	return err
}

// restartPlanStep stops the running app (not as a person's stop) and starts
// the plan's step again, which expands its placeholders anew.
func restartPlanStep(w io.Writer, base []string, root, instanceDir string, s knowledge.Step) error {
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return err
	}
	err = StopHost(w, instanceDir, s.Name, HostStopTimeout(), false)
	lock.Release()
	if err != nil {
		return err
	}
	return HostStart(w, w, base, root, instanceDir, s)
}
