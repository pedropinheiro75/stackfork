//go:build !windows

package lifecycle

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// sleepHost starts a real process the way a host app is recorded: its own
// group, its identity in HostProcs. It is killed when the test ends.
func sleepHost(t *testing.T) state.HostProc {
	t.Helper()
	c := exec.Command("sleep", "300")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	go c.Wait() //nolint:errcheck // reaped so a stopped host does not read as alive
	t.Cleanup(func() { syscall.Kill(-c.Process.Pid, syscall.SIGKILL) })
	started, err := state.ProcStart(c.Process.Pid)
	if err != nil {
		t.Skip("no process start time on this platform")
	}
	return state.HostProc{PID: c.Process.Pid, StartedAt: started, Cmdline: []string{"sleep", "300"}}
}

// cascadeInstance is an instance with postgres, worker (depends on postgres)
// and host apps api (postgres), app (api) and other (nothing), all running.
func cascadeInstance(t *testing.T, cascade bool) (dir string, procs map[string]state.HostProc) {
	t.Helper()
	root := t.TempDir()
	dir = t.TempDir()
	k := knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusDraft, Plan: knowledge.Plan{
		Status: knowledge.PlanRecorded, StopCascade: cascade,
		Steps: []knowledge.Step{hostStep("api", "postgres"), hostStep("app", "api"), hostStep("other")}}}
	b, _ := json.Marshal(k)
	if err := os.MkdirAll(filepath.Join(root, ".stackfork"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".stackfork", "knowledge.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	snap := map[string]any{"services": map[string]any{
		"postgres": map[string]any{"image": "postgres"},
		"worker":   map[string]any{"image": "w", "depends_on": map[string]any{"postgres": map[string]any{"condition": "service_started"}}},
	}}
	b, _ = json.Marshal(snap)
	os.WriteFile(filepath.Join(dir, "compose.json"), b, 0o600) //nolint:errcheck // the read below fails the test
	procs = map[string]state.HostProc{"api": sleepHost(t), "app": sleepHost(t), "other": sleepHost(t)}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), &state.Manifest{Project: "p", Root: root, Branch: "b", HostProcs: procs}); err != nil {
		t.Fatal(err)
	}
	return dir, procs
}

// stack is what Docker reports, by service; err makes Docker not answer.
type stack struct {
	states map[string]string
	err    error
}

func (st *stack) watcher(dir string) *CascadeWatcher {
	return &CascadeWatcher{
		Instances: func() []string { return []string{dir} },
		States: func(string) (map[string]string, error) {
			if st.err != nil {
				return nil, st.err
			}
			return st.states, nil
		},
	}
}

func ticks(w *CascadeWatcher, n int) {
	for i := 0; i < n; i++ {
		w.Tick()
	}
}

func alive(p state.HostProc) bool { return state.HostIdentity(p) }

var both = map[string]string{"postgres": "running", "worker": "running"}

func TestWatcherStopsDependentsOfAServiceSeenUpThenGoneAndRecordsIt(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	st := &stack{states: both}
	w := st.watcher(dir)
	w.Tick() // seen up
	st.states = map[string]string{"worker": "running"}
	w.Tick()
	if !alive(procs["api"]) || !alive(procs["app"]) {
		t.Fatal("one sighting must not stop anything")
	}
	w.Tick()
	if alive(procs["api"]) || alive(procs["app"]) {
		t.Fatal("api and app (through api) must be stopped after the second sighting")
	}
	if !alive(procs["other"]) {
		t.Fatal("a host app that does not depend on postgres stays")
	}
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if _, ok := m.HostProcs["api"]; !ok {
		t.Fatal("the row stays, so Start can respawn it")
	}
	if r, _ := state.ReadHostRecord(dir, "api"); r != nil && r.StoppedByPerson {
		t.Fatal("a cascade is not a person's stop: Start must bring api back")
	}
	r, err := runs.Get(dir, 1)
	if err != nil || r.Trigger != CascadeTrigger || r.Outcome != runs.Succeeded || len(r.Steps) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestStartBringsCascadedHostsBack(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	st := &stack{states: both}
	w := st.watcher(dir)
	w.Tick()
	st.states = map[string]string{"worker": "running"}
	ticks(w, 2)
	if alive(procs["api"]) {
		t.Fatal("api must be stopped")
	}
	var started []string
	old := HostStart
	HostStart = func(_, _ io.Writer, _ []string, _, _ string, s knowledge.Step) error {
		started = append(started, s.Name)
		return nil
	}
	t.Cleanup(func() { HostStart = old })
	plan := knowledge.Plan{Steps: []knowledge.Step{hostStep("api", "postgres"), hostStep("app", "api"), hostStep("other")}}
	base, main := t.TempDir(), t.TempDir()
	if _, err := TrustHere(base, main, plan); err != nil {
		t.Fatal(err)
	}
	rec, err := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli", Mode: knowledge.ModeHybrid, Project: "p", Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Finish(nil)
	if err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeHybrid, plan); err != nil {
		t.Fatal(err)
	}
	if strings.Join(started, ",") != "api,app,other" { // other is running: HostStart answers "already running"
		t.Fatalf("started %v", started)
	}
}

func TestWatcherDoesNotKillOnDoubt(t *testing.T) {
	cases := map[string]func(st *stack, w *CascadeWatcher){
		"a dependency never seen running (one-shot, profile)": func(st *stack, w *CascadeWatcher) {
			st.states = map[string]string{"worker": "running"}
			ticks(w, 6)
		},
		"a finished one-shot that was never seen up": func(st *stack, w *CascadeWatcher) {
			st.states = map[string]string{"postgres": "exited", "worker": "running"}
			ticks(w, 6)
		},
		"a container in created state after being seen up": func(st *stack, w *CascadeWatcher) {
			w.Tick()
			st.states = map[string]string{"postgres": "created", "worker": "running"}
			ticks(w, 6)
		},
		"Docker erroring between sightings": func(st *stack, w *CascadeWatcher) {
			w.Tick()
			st.states = map[string]string{"worker": "running"}
			w.Tick()
			st.err = errors.New("Cannot connect to the Docker daemon")
			w.Tick()
			st.err = nil
			w.Tick() // seen-up memory went with the error: nothing to lose yet
			w.Tick()
		},
		"Docker unreachable": func(st *stack, w *CascadeWatcher) {
			w.Tick()
			st.err = errors.New("daemon unreachable")
			ticks(w, 6)
		},
		"back between sightings": func(st *stack, w *CascadeWatcher) {
			w.Tick()
			st.states = map[string]string{"worker": "running"}
			w.Tick()
			st.states = both
			w.Tick()
			st.states = map[string]string{"worker": "running"}
			w.Tick()
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			dir, procs := cascadeInstance(t, true)
			st := &stack{states: both}
			run(st, st.watcher(dir))
			if !alive(procs["api"]) || !alive(procs["app"]) {
				t.Fatal("nothing may be stopped")
			}
			if _, err := runs.Get(dir, 1); err == nil {
				t.Fatal("no run is recorded when nothing was stopped")
			}
		})
	}
}

func TestWatcherStopsAnExitedDependencySeenUp(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	st := &stack{states: both}
	w := st.watcher(dir)
	w.Tick()
	st.states = map[string]string{"postgres": "exited", "worker": "running"}
	ticks(w, 2)
	if alive(procs["api"]) {
		t.Fatal("a database that was up and exited takes its apps down")
	}
}

// With no container of the project running the stack may be gone for good or
// Docker may be restarting: it takes 4 sightings, not 2.
func TestWatcherWholeStackGoneNeedsTheLongerWait(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	st := &stack{states: both}
	w := st.watcher(dir)
	w.Tick()
	st.states = map[string]string{}
	ticks(w, 3)
	if !alive(procs["api"]) {
		t.Fatal("3 sightings of an empty project are not enough")
	}
	w.Tick()
	if alive(procs["api"]) || alive(procs["app"]) {
		t.Fatal("the 4th stops them")
	}
}

func TestWatcherSkipsWhenOffOrBusy(t *testing.T) {
	// seenUp first, then the service goes: only the guard under test can save the app
	gone := map[string]string{"worker": "running"}
	drive := func(w *CascadeWatcher, st *stack, n int) {
		st.states = both
		w.Tick()
		st.states = gone
		ticks(w, n)
	}

	dir, procs := cascadeInstance(t, false)
	st := &stack{}
	drive(st.watcher(dir), st, 3)
	if !alive(procs["api"]) {
		t.Fatal("stop_cascade off: the watcher does nothing")
	}

	dir, procs = cascadeInstance(t, true)
	w := st.watcher(dir)
	w.ActionLocked = func(string) bool { return true }
	drive(w, st, 3)
	if !alive(procs["api"]) {
		t.Fatal("an action in flight: nothing is stopped")
	}

	w = st.watcher(dir)
	lock, err := state.TryLock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	drive(w, st, 3)
	lock.Release()
	if !alive(procs["api"]) {
		t.Fatal("the instance lock held: nothing is stopped")
	}

	rec, err := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli", Project: "p", Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	drive(st.watcher(dir), st, 3)
	rec.Finish(nil)
	if !alive(procs["api"]) {
		t.Fatal("a run in flight (a Start): nothing is stopped")
	}
}

// An action that starts while the watcher waits for Docker is seen again
// under the lock, and the service that came back is not lost.
func TestWatcherRechecksBeforeStopping(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	st := &stack{states: both}
	w := st.watcher(dir)
	w.Tick()
	st.states = map[string]string{"worker": "running"}
	w.Tick()
	calls := 0
	w.States = func(string) (map[string]string, error) {
		calls++
		if calls == 2 { // the recheck under the lock: it is back
			return both, nil
		}
		return st.states, nil
	}
	w.Tick()
	if calls < 2 || !alive(procs["api"]) {
		t.Fatalf("calls %d, api alive %v: a service back at the recheck must not stop its apps", calls, alive(procs["api"]))
	}
}

// rewrite gives the cascadeInstance a new plan (api's depends_on, and the
// steps before it) and a new snapshot.
func rewrite(t *testing.T, dir string, apiDeps []string, before []knowledge.Step, services map[string]any) {
	t.Helper()
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	steps := append(before, hostStep("api", apiDeps...), hostStep("app", "api"), hostStep("other"))
	k := knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusDraft,
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, StopCascade: true, Steps: steps}}
	b, _ := json.Marshal(k)
	if err := os.WriteFile(filepath.Join(m.Root, ".stackfork", "knowledge.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(map[string]any{"services": services})
	if err := os.WriteFile(filepath.Join(dir, "compose.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A job (no ports, no healthcheck) that exited 0 finished; a server (ports)
// that exited 0, like postgres after `docker stop`, is down.
func TestWatcherJobThatExitedZeroIsFinishedButAServerIsDown(t *testing.T) {
	svcs := map[string]any{
		"postgres": map[string]any{"image": "postgres", "ports": []any{map[string]any{"target": 5432}}},
		"migrate":  map[string]any{"image": "m"},
	}
	up := map[string]string{"postgres": "running", "migrate": "running"}

	dir, procs := cascadeInstance(t, true)
	rewrite(t, dir, []string{"postgres", "migrate"}, nil, svcs)
	st := &stack{states: up}
	w := st.watcher(dir)
	w.Tick()
	st.states = map[string]string{"postgres": "running", "migrate": ExitedZero}
	ticks(w, 3)
	st.states = map[string]string{"postgres": "running"} // the finished job's container removed later
	ticks(w, 6)
	if !alive(procs["api"]) {
		t.Fatal("a finished job is not down")
	}

	st.states = up
	w.Tick()
	st.states = map[string]string{"postgres": ExitedZero, "migrate": ExitedZero}
	ticks(w, stackGoneTicks) // nothing runs: the longer wait
	if alive(procs["api"]) {
		t.Fatal("a server that exited 0 was stopped: its apps go too")
	}
}

// A service awaited with service_completed_successfully is a job even with ports.
func TestOneShots(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]any{"services": map[string]any{
		"seed": map[string]any{"ports": []any{"1"}},
		"api":  map[string]any{"ports": []any{"2"}, "depends_on": map[string]any{"seed": map[string]any{"condition": "service_completed_successfully"}}},
		"db":   map[string]any{"healthcheck": map[string]any{"test": "x"}},
		"init": map[string]any{},
	}})
	os.WriteFile(filepath.Join(dir, "compose.json"), b, 0o600) //nolint:errcheck // checked below
	got := oneShots(dir)
	if !got["seed"] || !got["init"] || got["api"] || got["db"] {
		t.Fatalf("oneShots = %v", got)
	}
}

// The service a mode's plan leaves out (a hybrid host app replaces it) is not
// watched: its container stopping takes nothing down.
func TestWatcherIgnoresAServiceThePlanDoesNotStart(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	rewrite(t, dir, []string{"postgres", "web"}, []knowledge.Step{{ID: "deps", Kind: knowledge.KindComposeUp, Exclude: []string{"web"}}},
		map[string]any{"postgres": map[string]any{"image": "postgres"}, "web": map[string]any{"image": "w"}})
	st := &stack{states: map[string]string{"postgres": "running", "web": "running"}}
	w := st.watcher(dir)
	w.Tick()
	st.states = map[string]string{"postgres": "running", "web": "exited"}
	ticks(w, 6)
	if !alive(procs["api"]) {
		t.Fatal("a service the plan does not start is not the stack's")
	}
}

// After Reset (Docker was out of reach, or a resume ran) a service must be
// seen running again before it can be lost.
func TestWatcherResetForgetsSightings(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	st := &stack{states: both}
	w := st.watcher(dir)
	w.Tick()
	w.Reset()
	st.states = map[string]string{"worker": "running"}
	ticks(w, 6)
	if !alive(procs["api"]) {
		t.Fatal("nothing seen since the reset: nothing is lost")
	}
}

func TestDockerStatesMarksACleanExit(t *testing.T) {
	bin := t.TempDir()
	out := "migrate exited Exited (0) 3 minutes ago\npg exited Exited (137) 1 minute ago\napi running Up 2 minutes\napi exited Exited (1) 1 hour ago\n"
	os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf '"+out+"'\n"), 0o755) //nolint:errcheck // the call below fails
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	st, err := DockerStates("p")
	if err != nil || st["migrate"] != ExitedZero || st["pg"] != "exited" || st["api"] != "running" {
		t.Fatalf("%v %v", st, err)
	}
}
