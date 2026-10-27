package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// CascadeTrigger is the trigger (and command) of the run a watcher stop records.
const CascadeTrigger = "cascade"

// CascadeEvery is the watcher's tick; a service must be missing on two
// consecutive ticks, so a host app outlives its dependency by 15 to 30 s.
const CascadeEvery = 15 * time.Second

// CascadeWatcher stops the host apps whose Compose dependency vanished
// without a stop going through stackfork (a container removed, a Docker
// reset). The one failure mode it has is doing nothing: any doubt (Docker
// errors, a run or action in flight, a lock held, a service seen running
// again) means no stop, and a stop needs two clean sightings in a row.
type CascadeWatcher struct {
	Instances    func() []string                               // instance directories
	Running      func(project string) (map[string]bool, error) // running services; nil: DockerRunning
	ActionLocked func(project string) bool                     // the dashboard's per-project action lock; nil: never
	Every        time.Duration                                 // 0: CascadeEvery
	NewBase      string                                        // state bases, for the proxy reload after a stop
	LegacyBase   string

	missing map[string]int // dir + "\x00" + service -> consecutive ticks seen missing
}

// InstanceDirs lists every instance directory under both state bases.
func InstanceDirs(newBase, legacyBase string) []string {
	var dirs []string
	for _, b := range []struct{ base, prefix string }{{newBase, identity.Prefix}, {legacyBase, identity.LegacyPrefix}} {
		ms, _ := state.Manifests(b.base, b.prefix)
		for _, m := range ms {
			dirs = append(dirs, filepath.Dir(m))
		}
	}
	return dirs
}

// DockerRunning lists the services of project with a running container. An
// error means Docker did not answer, which is never "the service is gone".
func DockerRunning(project string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "--filter", "label=com.docker.compose.project="+project,
		"--format", `{{.Label "com.docker.compose.service"}}`).Output()
	if err != nil {
		return nil, err
	}
	run := map[string]bool{}
	for _, n := range strings.Fields(string(out)) {
		run[n] = true
	}
	return run, nil
}

// Run ticks until ctx ends.
func (w *CascadeWatcher) Run(ctx context.Context) {
	every := w.Every
	if every <= 0 {
		every = CascadeEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Tick()
		}
	}
}

// Tick looks at every instance once.
func (w *CascadeWatcher) Tick() {
	if w.missing == nil {
		w.missing = map[string]int{}
	}
	for _, dir := range w.Instances() {
		w.watch(dir)
	}
}

func (w *CascadeWatcher) forget(dir string) {
	for k := range w.missing {
		if strings.HasPrefix(k, dir+"\x00") {
			delete(w.missing, k)
		}
	}
}

func (w *CascadeWatcher) watch(dir string) {
	m, err := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil || m == nil {
		w.forget(dir)
		return
	}
	k, err := knowledge.LoadForCheckout(m.Root)
	if err != nil || k == nil || !k.Plan.StopCascade {
		w.forget(dir)
		return
	}
	// the services the running host apps name as their direct dependency
	deps := composeDeps(dir)
	apps := map[string]bool{}
	for _, s := range k.Plan.HostSteps() {
		apps[s.Name] = true
	}
	wanted := map[string]bool{}
	for _, s := range k.Plan.HostSteps() {
		if proc, ok := m.HostProcs[s.Name]; !ok || !state.HostIdentity(proc) {
			continue
		}
		for _, d := range s.DependsOn {
			if _, ok := deps[d]; ok && !strings.HasPrefix(d, hostPrefix) {
				wanted[d] = true
			}
		}
	}
	// a run, an action or a lock in flight (a Start brings services up in
	// turns): look again next tick, and count nothing from this one
	if len(wanted) == 0 || w.busy(dir, m.Project) {
		w.forget(dir)
		return
	}
	running := w.Running
	if running == nil {
		running = DockerRunning
	}
	up, err := running(m.Project)
	if err != nil {
		w.forget(dir) // two answers in a row, or nothing
		return
	}
	var lost []string
	for svc := range wanted {
		key := dir + "\x00" + svc
		if up[svc] {
			delete(w.missing, key)
			continue
		}
		if w.missing[key]++; w.missing[key] >= 2 {
			lost = append(lost, svc)
		}
	}
	if len(lost) == 0 {
		return
	}
	_, hosts := Dependents(k.Plan, deps, lost)
	w.stop(dir, m, lost, hosts)
	w.forget(dir)
}

func (w *CascadeWatcher) busy(dir, project string) bool {
	return (w.ActionLocked != nil && w.ActionLocked(project)) || runs.Busy(dir) || state.Held(filepath.Join(dir, ".lock"))
}

// stop records a run and stops the running ones of hosts, keeping their rows.
func (w *CascadeWatcher) stop(dir string, m *state.Manifest, lost, hosts []string) {
	rec, err := runs.Begin(dir, runs.Meta{Command: CascadeTrigger, Trigger: CascadeTrigger, Mode: knowledge.ModeHybrid,
		Project: m.Project, Branch: m.Branch, NoWait: true})
	if err != nil {
		return // another run owns the instance
	}
	lock, err := state.TryLock(filepath.Join(dir, ".lock"))
	if err != nil {
		rec.Finish(errors.New("the instance was busy; nothing was stopped"))
		return
	}
	var errs []error
	for _, n := range hosts {
		proc, ok := m.HostProcs[n]
		if !ok || !state.HostIdentity(proc) {
			continue
		}
		st := rec.Step("host-"+n, knowledge.KindHost, "stop host app "+n)
		sw := st.Start()
		fmt.Fprintf(sw, "%s is not running, and %s needs it; stopping %s\n", strings.Join(lost, ", "), n, n)
		err := StopHost(sw, dir, n, HostStopTimeout(), true)
		st.End(err, 0)
		errs = append(errs, err)
	}
	lock.Release()
	rec.Finish(errors.Join(errs...))
	ReloadProxyIfRunning(io.Discard, w.NewBase, w.LegacyBase)
}
