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

// Sightings a lost service needs before the watcher acts. With other
// containers of the project still running a missing one is a plain loss (2
// ticks, 30 s). With none running the whole stack is gone, which is also what
// a Docker Desktop restart looks like for a moment, so it takes 4 (60 s).
const (
	lostTicks      = 2
	stackGoneTicks = 4
)

// CascadeWatcher stops the host apps whose Compose dependency vanished
// without a stop going through stackfork (a container removed, a Docker
// reset). The one failure mode it has is doing nothing: any doubt (Docker
// errors, a run or action in flight, a lock held, a service seen running
// again) means no stop. A service counts as lost only after this process saw
// it running and then found its container exited, dead or gone, so a finished
// one-shot, a profile-gated service nobody started and a container still in
// created state are never "lost".
type CascadeWatcher struct {
	Instances    func() []string                                 // instance directories
	States       func(project string) (map[string]string, error) // container state by service; nil: DockerStates
	ActionLocked func(project string) bool                       // the dashboard's per-project action lock; nil: never
	Every        time.Duration                                   // 0: CascadeEvery
	NewBase      string                                          // state bases, for the proxy reload after a stop
	LegacyBase   string

	missing map[string]int  // dir + "\x00" + service -> consecutive ticks seen lost
	seenUp  map[string]bool // same key: seen running by this process
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

// DockerStates maps each service of project to the state of its container
// (running wins over the others), from `docker ps -a`; a service with no
// container is absent. An error means Docker did not answer, which is never
// "the service is gone".
func DockerStates(project string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+project,
		"--format", `{{.Label "com.docker.compose.service"}} {{.State}}`).Output()
	if err != nil {
		return nil, err
	}
	st := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) == 2 && st[f[0]] != "running" {
			st[f[0]] = f[1]
		}
	}
	return st, nil
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
		w.missing, w.seenUp = map[string]int{}, map[string]bool{}
	}
	for _, dir := range w.Instances() {
		w.watch(dir)
	}
}

// forget drops everything known about dir: after doubt, sightings start over.
func (w *CascadeWatcher) forget(dir string) {
	for k := range w.seenUp {
		if strings.HasPrefix(k, dir+"\x00") {
			delete(w.seenUp, k)
		}
	}
	for k := range w.missing {
		if strings.HasPrefix(k, dir+"\x00") {
			delete(w.missing, k)
		}
	}
}

// isLost: svc was seen running by this process and its container is now
// exited, dead or gone. Created, restarting, paused and the like are doubt.
func (w *CascadeWatcher) isLost(key string, states map[string]string, svc string) bool {
	if !w.seenUp[key] {
		return false
	}
	switch states[svc] {
	case "", "exited", "dead":
		return true
	}
	return false
}

func anyRunning(states map[string]string) bool {
	for _, s := range states {
		if s == "running" {
			return true
		}
	}
	return false
}

func (w *CascadeWatcher) states(project string) (map[string]string, error) {
	if w.States != nil {
		return w.States(project)
	}
	return DockerStates(project)
}

func (w *CascadeWatcher) watch(dir string) {
	m, err := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil || m == nil {
		w.forget(dir)
		return
	}
	// LoadFirst, not LoadFor: no `git worktree list` every tick for an
	// instance whose project has no knowledge file.
	k, err := knowledge.LoadFirst(m.Root, identity.MainRepoOf(m.Root))
	if err != nil || k == nil || !k.Plan.StopCascade {
		w.forget(dir)
		return
	}
	// the services the running host apps name as their direct dependency
	deps := composeDeps(dir)
	wanted := map[string]bool{}
	for _, s := range k.Plan.HostSteps() {
		if proc, ok := m.HostProcs[s.Name]; !ok || !state.HostIdentity(proc) {
			continue
		}
		for _, d := range s.DependsOn {
			if _, ok := deps[d]; ok {
				wanted[d] = true
			}
		}
	}
	// a run, an action or a lock in flight (a Start brings services up in
	// turns): look again later, and keep nothing from before
	if len(wanted) == 0 || w.busy(dir, m.Project) {
		w.forget(dir)
		return
	}
	states, err := w.states(m.Project)
	if err != nil {
		w.forget(dir) // Docker did not answer: what was seen before is in doubt
		return
	}
	need := lostTicks
	if !anyRunning(states) {
		need = stackGoneTicks
	}
	var lost []string
	for svc := range wanted {
		key := dir + "\x00" + svc
		switch {
		case states[svc] == "running":
			w.seenUp[key] = true
			delete(w.missing, key)
		case !w.isLost(key, states, svc):
			delete(w.missing, key)
		default:
			if w.missing[key]++; w.missing[key] >= need {
				lost = append(lost, svc)
			}
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

// stop takes the instance lock, then looks again (the check above is up to a
// `docker ps` old): only a service still lost, with nothing in flight, takes
// its dependents down. It records a run and keeps the rows of the host apps.
func (w *CascadeWatcher) stop(dir string, m *state.Manifest, lost, hosts []string) {
	lock, err := state.TryLock(filepath.Join(dir, ".lock"))
	if err != nil {
		return
	}
	defer lock.Release()
	if w.ActionLocked != nil && w.ActionLocked(m.Project) {
		return
	}
	states, err := w.states(m.Project)
	if err != nil {
		return
	}
	still := lost[:0:0]
	for _, svc := range lost {
		if w.isLost(dir+"\x00"+svc, states, svc) {
			still = append(still, svc)
		}
	}
	if len(still) == 0 {
		return
	}
	rec, err := runs.Begin(dir, runs.Meta{Command: CascadeTrigger, Trigger: CascadeTrigger, Mode: knowledge.ModeHybrid,
		Project: m.Project, Branch: m.Branch, NoWait: true})
	if err != nil {
		return // another run owns the instance
	}
	var errs []error
	for _, n := range hosts {
		proc, ok := m.HostProcs[n]
		if !ok || !state.HostIdentity(proc) {
			continue
		}
		st := rec.Step("host-"+n, knowledge.KindHost, "stop host app "+n)
		sw := st.Start()
		fmt.Fprintf(sw, "%s is not running, and %s needs it; stopping %s\n", strings.Join(still, ", "), n, n)
		err := stopHost(sw, dir, n, HostStopTimeout(), true, false) // a cascade, not a person: Start brings it back
		st.End(err, 0)
		errs = append(errs, err)
	}
	rec.Finish(errors.Join(errs...))
	ReloadProxyIfRunning(io.Discard, w.NewBase, w.LegacyBase)
}
