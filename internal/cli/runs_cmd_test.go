package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// twoRuns records a succeeded run and a failed one (seed fails) in dir and
// pins the clock five minutes after the second one started.
func twoRuns(t *testing.T, dir string) {
	t.Helper()
	a, err := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli", Mode: "container"})
	if err != nil {
		t.Fatal(err)
	}
	s := a.Step("up", "compose-up", "start the stack")
	s.Start().Write([]byte("started\n"))
	s.End(nil, 0)
	a.Finish(nil)

	b, err := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "dashboard", Mode: "hybrid"})
	if err != nil {
		t.Fatal(err)
	}
	b.Step("up", "compose-up", "start the stack").Skip("already running")
	sd := b.Step("seed", "exec", "load the demo data")
	sd.Start().Write([]byte("boom: 42 rows\n"))
	sd.End(errors.New("exit 3"), 3)
	b.Step("app", "host", "start the app")
	b.Finish(errors.New("seed failed"))
	r, _ := runs.Get(dir, 2)
	runsNow = func() time.Time { return r.StartedAt.Add(5 * time.Minute) }
	t.Cleanup(func() { runsNow = time.Now })
}

func runsCmd(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code, err := cmdRuns(&out, &errb, args, "sf-x", dir)
	if err != nil {
		return out.String(), err.Error(), 1
	}
	return out.String(), errb.String(), code
}

func TestRunsListTextAndJSON(t *testing.T) {
	dir := t.TempDir()
	if out, _, code := runsCmd(t, dir); code != 0 || out != "no runs yet for sf-x\n" {
		t.Fatalf("empty: %d %q", code, out)
	}
	if out, _, _ := runsCmd(t, dir, "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty json: %q", out)
	}
	twoRuns(t, dir)
	out, _, code := runsCmd(t, dir)
	if code != 0 {
		t.Fatal(out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %q", out)
	}
	want2 := []string{"2", "start-env", "dashboard", "hybrid", "failed", "5m ago", "seed"}
	want1 := []string{"1", "start-env", "cli", "container", "succeeded", "5m ago"}
	if strings.Join(strings.Fields(lines[1])[:5], " ") != strings.Join(want2[:5], " ") || !strings.HasSuffix(lines[1], "seed") ||
		strings.Join(strings.Fields(lines[2])[:5], " ") != strings.Join(want1[:5], " ") {
		t.Errorf("rows:\n%s", out)
	}
	if out, _, _ := runsCmd(t, dir, "--limit", "1"); strings.Count(out, "\n") != 2 {
		t.Errorf("--limit 1:\n%s", out)
	}
	var got []map[string]any
	js, _, _ := runsCmd(t, dir, "--json")
	if err := json.Unmarshal([]byte(js), &got); err != nil || len(got) != 2 || got[0]["id"] != float64(2) {
		t.Fatalf("json: %v %s", err, js)
	}
	if _, ok := got[0]["duration_ms"]; !ok && got[0]["ended_at"] == nil {
		t.Error("duration_ms missing")
	}
}

func TestRunsShow(t *testing.T) {
	dir := t.TempDir()
	twoRuns(t, dir)
	out, _, code := runsCmd(t, dir, "show")
	if code != 0 || !strings.Contains(out, "run 2") || !strings.Contains(out, "failed at seed") ||
		!strings.Contains(out, "skipped") || !strings.Contains(out, "already running") ||
		!strings.Contains(out, "pending") || !strings.Contains(out, "seed failed") {
		t.Fatalf("show latest:\n%s", out)
	}
	if out, _, _ := runsCmd(t, dir, "show", "1"); !strings.Contains(out, "run 1") || !strings.Contains(out, "succeeded") {
		t.Errorf("show 1:\n%s", out)
	}
	if out, _, _ := runsCmd(t, dir, "show", "--step", "seed"); out != "boom: 42 rows\nerror: exit 3\n" {
		t.Errorf("step log: %q", out)
	}
	if out, _, _ := runsCmd(t, dir, "show", "1", "--step", "up"); out != "started\n" {
		t.Errorf("step log of 1: %q", out)
	}
	var one map[string]any
	js, _, _ := runsCmd(t, dir, "show", "--json")
	if err := json.Unmarshal([]byte(js), &one); err != nil || one["id"] != float64(2) {
		t.Fatalf("json: %v %s", err, js)
	}
	for _, args := range [][]string{{"show", "9"}, {"show", "x"}, {"show", "--step", "nope"}, {"show", "--step", "app"}} {
		if _, e, code := runsCmd(t, dir, args...); code == 0 {
			t.Errorf("%v: want an error, stderr %q", args, e)
		}
	}
	if _, e, code := runsCmd(t, t.TempDir(), "show"); code == 0 || !strings.Contains(e, "no runs yet for sf-x") {
		t.Errorf("show without runs: %d %q", code, e)
	}
}

func TestRunsWithInstanceFromAnotherDirectory(t *testing.T) {
	_, _, instance, flags, _ := worktreeFixture(t)
	dir := filepath.Join(flags[1], instance)
	r, _ := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli"})
	r.Finish(nil)
	out, e, code := runCLI(t, append(flags, "--root", t.TempDir(), "--instance", instance, "runs")...)
	if code != 0 || !strings.Contains(out, "up") || !strings.Contains(out, "succeeded") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	out, e, code = runCLI(t, append(flags, "runs", "show", "--json", "--instance", instance)...)
	if code != 0 || !strings.Contains(out, `"command": "up"`) && !strings.Contains(out, `"command":"up"`) {
		t.Fatalf("%d %s %s", code, out, e)
	}
}
