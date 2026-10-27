//go:build !windows

package lifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

// watcher answers from running (nil error) and counts the Docker calls.
func watcher(dir string, running *map[string]bool, dockerErr *error) *CascadeWatcher {
	return &CascadeWatcher{
		Instances: func() []string { return []string{dir} },
		Running: func(string) (map[string]bool, error) {
			if *dockerErr != nil {
				return nil, *dockerErr
			}
			return *running, nil
		},
	}
}

func alive(p state.HostProc) bool { return state.HostIdentity(p) }

func TestWatcherStopsDependentsAfterTwoTicksAndRecordsIt(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	running, dockerErr := map[string]bool{"worker": true}, error(nil) // postgres is gone
	w := watcher(dir, &running, &dockerErr)
	w.Tick()
	if !alive(procs["api"]) || !alive(procs["app"]) {
		t.Fatal("one sighting must not stop anything")
	}
	w.Tick()
	if alive(procs["api"]) || alive(procs["app"]) {
		t.Fatal("api and app (through api) must be stopped after the second tick")
	}
	if !alive(procs["other"]) {
		t.Fatal("a host app that does not depend on postgres stays")
	}
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if _, ok := m.HostProcs["api"]; !ok {
		t.Fatal("the row stays, so Start can respawn it")
	}
	r, err := runs.Get(dir, 1)
	if err != nil || r.Trigger != CascadeTrigger || r.Outcome != runs.Succeeded || len(r.Steps) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestWatcherSeesTheServiceBackBetweenTicks(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	running, dockerErr := map[string]bool{}, error(nil)
	w := watcher(dir, &running, &dockerErr)
	w.Tick()
	running["postgres"] = true
	w.Tick()
	delete(running, "postgres")
	w.Tick() // the count started again
	if !alive(procs["api"]) {
		t.Fatal("two sightings must be consecutive")
	}
}

func TestWatcherNeverActsOnADockerError(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	running, dockerErr := map[string]bool{}, error(nil)
	w := watcher(dir, &running, &dockerErr)
	w.Tick()
	dockerErr = errors.New("Cannot connect to the Docker daemon")
	w.Tick()
	dockerErr = nil
	w.Tick() // the error broke the run of sightings: this is the first again
	if !alive(procs["api"]) || !alive(procs["app"]) {
		t.Fatal("a Docker error between two sightings must not count")
	}
	dockerErr = errors.New("daemon unreachable")
	for i := 0; i < 5; i++ {
		w.Tick()
	}
	if !alive(procs["api"]) {
		t.Fatal("an unreachable Docker never stops anything")
	}
	if _, err := runs.Get(dir, 1); err == nil {
		t.Fatal("no run is recorded when nothing was stopped")
	}
}

func TestWatcherSkipsWhenOffOrBusy(t *testing.T) {
	missing := map[string]bool{}
	var noErr error

	dir, procs := cascadeInstance(t, false)
	w := watcher(dir, &missing, &noErr)
	w.Tick()
	w.Tick()
	if !alive(procs["api"]) {
		t.Fatal("stop_cascade off: the watcher does nothing")
	}

	dir, procs = cascadeInstance(t, true)
	w = watcher(dir, &missing, &noErr)
	w.ActionLocked = func(string) bool { return true }
	w.Tick()
	w.Tick()
	if !alive(procs["api"]) {
		t.Fatal("an action in flight: nothing is stopped")
	}

	w = watcher(dir, &missing, &noErr)
	lock, err := state.TryLock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	w.Tick()
	w.Tick()
	lock.Release()
	if !alive(procs["api"]) {
		t.Fatal("the instance lock held: nothing is stopped")
	}

	rec, err := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli", Project: "p", Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	w.Tick()
	w.Tick()
	rec.Finish(nil)
	if !alive(procs["api"]) {
		t.Fatal("a run in flight (a Start): nothing is stopped")
	}
	// the busy ticks counted for nothing: it takes two fresh ones
	w.Tick()
	if !alive(procs["api"]) {
		t.Fatal("the first free tick is the first sighting")
	}
	w.Tick()
	if alive(procs["api"]) {
		t.Fatal("two free ticks stop it")
	}
}

func TestWatcherLeavesAHostThatDoesNotNeedTheMissingService(t *testing.T) {
	dir, procs := cascadeInstance(t, true)
	running, noErr := map[string]bool{"postgres": true}, error(nil)
	w := watcher(dir, &running, &noErr)
	w.Tick()
	w.Tick()
	// worker is not a dependency of any host app, so it being down is not ours
	if !alive(procs["api"]) || !alive(procs["app"]) || !alive(procs["other"]) {
		t.Fatal("nothing was lost")
	}
}
