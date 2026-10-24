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
	// what a real run records: api was a server that a signal ended
	setRecord(t, dir, "api", state.HostRecord{Name: "api", SupervisorPID: 999999, Listened: true, ExitedAt: "2026-10-23T17:12:00Z", ExitCode: intp(129)})
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

func intp(n int) *int { return &n }

func setRecord(t *testing.T, dir, name string, r state.HostRecord) {
	t.Helper()
	r.Name = name
	if err := state.WriteHostRecord(dir, r); err != nil {
		t.Fatal(err)
	}
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

func TestStartHostAppsStartsNothingInContainerMode(t *testing.T) {
	plan := knowledge.Plan{Steps: []knowledge.Step{{ID: "ui", Kind: knowledge.KindHost, Name: "ui", Cmd: "bun dev"}}}
	base, main := t.TempDir(), t.TempDir()
	if _, err := TrustHere(base, main, plan); err != nil {
		t.Fatal(err)
	}
	dir, rec, restarted, started := hostAppsEnv(t)
	if err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeContainer, plan); err != nil {
		t.Fatal(err)
	}
	if len(*started) != 0 || len(*restarted) != 0 {
		t.Errorf("container mode starts no host app: restarted %v started %v", *restarted, *started)
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

func TestStartHostAppsRestartsOnlyAServerThatEndedByItself(t *testing.T) {
	for name, tc := range map[string]struct {
		rec  *state.HostRecord
		want bool
	}{
		"killed by a signal":                 {&state.HostRecord{Listened: true, ExitedAt: "x", ExitCode: intp(129)}, true},
		"crashed with a code":                {&state.HostRecord{Listened: true, ExitedAt: "x", ExitCode: intp(1)}, true},
		"supervisor killed, no exit written": {&state.HostRecord{Listened: true}, true},
		"clean exit 0":                       {&state.HostRecord{Listened: true, ExitedAt: "x", ExitCode: intp(0)}, false},
		"stopped by a person":                {&state.HostRecord{Listened: true, StoppedByPerson: true, ExitedAt: "x", ExitCode: intp(143)}, false},
		"never listened (a one-shot)":        {&state.HostRecord{ExitedAt: "x", ExitCode: intp(1)}, false},
		"no record":                          {nil, false},
	} {
		dir, rec, restarted, _ := hostAppsEnv(t)
		os.Remove(state.HostRecordPath(dir, "api"))
		if tc.rec != nil {
			r := *tc.rec
			r.SupervisorPID = 999999
			setRecord(t, dir, "api", r)
		}
		if err := StartHostApps(rec, io.Discard, nil, "/r", "/r", dir, t.TempDir(), "", knowledge.ModeHybrid, knowledge.Plan{}); err != nil {
			t.Fatal(err)
		}
		if got := len(*restarted) == 1; got != tc.want {
			t.Errorf("%s: restarted=%v, want %v", name, *restarted, tc.want)
		}
		rec.Finish(nil)
	}
}

func TestStartHostAppsPlanCommandBeatsTheRecordedOne(t *testing.T) {
	plan := knowledge.Plan{Steps: []knowledge.Step{{ID: "api", Kind: knowledge.KindHost, Name: "api", Cmd: "bun run dev:api"}}}
	base, main := t.TempDir(), t.TempDir()
	if _, err := TrustHere(base, main, plan); err != nil {
		t.Fatal(err)
	}
	dir, rec, restarted, started := hostAppsEnv(t)
	if err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeHybrid, plan); err != nil {
		t.Fatal(err)
	}
	if len(*restarted) != 0 || len(*started) != 1 || (*started)[0] != "api" {
		t.Errorf("the plan's api step must run, not the recorded command: restarted %v started %v", *restarted, *started)
	}
	// but not when a person stopped it
	dir, rec, restarted, started = hostAppsEnv(t)
	setRecord(t, dir, "api", state.HostRecord{SupervisorPID: 999999, Listened: true, StoppedByPerson: true})
	if err := StartHostApps(rec, io.Discard, nil, "/r", main, dir, base, "", knowledge.ModeHybrid, plan); err != nil {
		t.Fatal(err)
	}
	if len(*restarted)+len(*started) != 0 {
		t.Errorf("a stopped app stays stopped: restarted %v started %v", *restarted, *started)
	}
}
