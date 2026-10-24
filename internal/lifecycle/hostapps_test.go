package lifecycle

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// hostAppsEnv prepares an instance dir whose manifest records host apps
// "api" (exited, restartable), "web" (running: this process) and "fg" (exited,
// started in the foreground, so not restartable), stubs the start hooks and
// opens a run.
func hostAppsEnv(t *testing.T) (dir string, rec *runs.Run, restarted, started *[]string) {
	t.Helper()
	dir = t.TempDir()
	self, err := state.ProcStart(os.Getpid())
	if err != nil {
		t.Skip("no process start time on this platform")
	}
	dead := state.HostProc{PID: 999999, StartedAt: "1970-01-01T00:00:00Z", Port: 40001,
		Cmdline: []string{"stackfork", "host", "--supervise", "api", "--", "bun", "dev"}}
	m := &state.Manifest{Project: "p", HostProcs: map[string]state.HostProc{
		"api": dead,
		"web": {PID: os.Getpid(), StartedAt: self, Cmdline: os.Args, Port: 40002},
		"fg":  {PID: 999998, StartedAt: "1970-01-01T00:00:00Z", Cmdline: []string{"stackfork", "host", "fg", "--", "x"}, Port: 40003},
	}}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	restarted, started = &[]string{}, &[]string{}
	oldR, oldS := HostRestart, HostStart
	HostRestart = func(_ io.Writer, _, _, name, _, _ string) error { *restarted = append(*restarted, name); return nil }
	HostStart = func(_, _ io.Writer, _ []string, _, _ string, st knowledge.Step) error {
		*started = append(*started, st.Name)
		if st.Name == "boom" {
			return errors.New("did not listen")
		}
		return nil
	}
	t.Cleanup(func() { HostRestart, HostStart = oldR, oldS })
	rec, err = runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli", Mode: knowledge.ModeHybrid, Project: "p", Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return dir, rec, restarted, started
}

func stepStates(t *testing.T, dir string, rec *runs.Run, err error) map[string]string {
	t.Helper()
	rec.Finish(err)
	r, gerr := runs.Get(dir, rec.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	out := map[string]string{}
	for _, s := range r.Steps {
		if s.Kind != knowledge.KindHost {
			t.Errorf("step %s kind %s", s.ID, s.Kind)
		}
		out[s.ID] = s.State
	}
	return out
}

func TestStartHostAppsRestartsRecordedAppsThatAreDown(t *testing.T) {
	dir, rec, restarted, started := hostAppsEnv(t)
	err := StartHostApps(rec, io.Discard, nil, "/r", "/r", dir, t.TempDir(), "", knowledge.ModeHybrid, knowledge.Plan{})
	if err != nil {
		t.Fatal(err)
	}
	if len(*restarted) != 1 || (*restarted)[0] != "api" || len(*started) != 0 {
		t.Fatalf("restarted %v started %v", *restarted, *started)
	}
	got := stepStates(t, dir, rec, nil)
	if got["host-api"] != runs.Succeeded || got["host-web"] != runs.Skipped || got["host-fg"] != runs.Skipped || len(got) != 3 {
		t.Errorf("steps %v", got)
	}
}

func TestStartHostAppsPlanStepsOnlyInHybridMode(t *testing.T) {
	plan := knowledge.Plan{Steps: []knowledge.Step{{ID: "ui", Kind: knowledge.KindHost, Name: "ui", Cmd: "bun dev"}}}
	base, main := t.TempDir(), t.TempDir()
	if _, err := TrustHere(base, main, plan); err != nil {
		t.Fatal(err)
	}
	dir, rec, restarted, started := hostAppsEnv(t)
	if err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeContainer, plan); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 0 || len(*restarted) != 1 {
		t.Errorf("a recorded app restarts in any mode, a plan host step only in hybrid: restarted %v started %v", *restarted, *started)
	}
}

func TestStartHostAppsStartsPlanHostStepsWithoutARecordOnlyWhenTrusted(t *testing.T) {
	plan := knowledge.Plan{Steps: []knowledge.Step{
		{ID: "ui", Kind: knowledge.KindHost, Name: "ui", Cmd: "bun dev"},
	}}
	base, main := t.TempDir(), t.TempDir()

	dir, rec, restarted, started := hostAppsEnv(t)
	err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeHybrid, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(*restarted)+len(*started) != 0 {
		t.Fatalf("an untrusted plan must run no host command: restarted %v started %v", *restarted, *started)
	}
	got := stepStates(t, dir, rec, nil)
	for id, st := range got {
		if st != runs.Skipped {
			t.Errorf("%s: %s, want skipped", id, st)
		}
	}

	if _, err := TrustHere(base, main, plan); err != nil {
		t.Fatal(err)
	}
	dir, rec, restarted, started = hostAppsEnv(t)
	if err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeHybrid, plan); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 1 || (*started)[0] != "ui" || len(*restarted) != 1 {
		t.Fatalf("restarted %v started %v", *restarted, *started)
	}
	if got := stepStates(t, dir, rec, nil); got["ui"] != runs.Succeeded {
		t.Errorf("steps %v", got)
	}
}

func TestStartHostAppsFailureIsOneStepsAndTheRunsError(t *testing.T) {
	plan := knowledge.Plan{Steps: []knowledge.Step{{ID: "boom", Kind: knowledge.KindHost, Name: "boom", Cmd: "x"}}}
	base, main := t.TempDir(), t.TempDir()
	if _, err := TrustHere(base, main, plan); err != nil {
		t.Fatal(err)
	}
	dir, rec, _, _ := hostAppsEnv(t)
	err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeHybrid, plan)
	if err == nil {
		t.Fatal("a host app that does not start must fail the up")
	}
	if got := stepStates(t, dir, rec, err); got["boom"] != runs.Failed || got["host-api"] != runs.Succeeded {
		t.Errorf("steps %v", got)
	}
}
