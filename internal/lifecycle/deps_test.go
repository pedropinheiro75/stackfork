package lifecycle

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func depsRun(t *testing.T, root, instanceDir, base string, plan knowledge.Plan) error {
	t.Helper()
	return PlanRunner{Out: io.Discard, Err: io.Discard, Exact: true, NewBase: base, Main: root, Root: root, InstanceDir: instanceDir,
		Mode: knowledge.ModeContainer, Plan: plan}.Execute()
}

// An exec step with once: deps runs when the lockfiles changed since its last
// success, and the hash is stored only on success.
func TestDepsStepRunsOnLockfileChange(t *testing.T) {
	root, instanceDir, base := t.TempDir(), t.TempDir(), t.TempDir()
	if err := state.WritePrivate(filepath.Join(instanceDir, "manifest.json"), &state.Manifest{Project: "p"}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "apps", "api"), 0o755)
	write := func(rel, s string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("yarn.lock", "v1")
	plan := knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "install", Kind: knowledge.KindExec, On: knowledge.OnHost, Dir: "apps/api", Once: knowledge.OnceDeps, Cmd: "yarn install --frozen-lockfile"},
		{ID: "build", Kind: knowledge.KindNote, Text: "n"},
	}}
	if _, err := TrustHere(base, root, plan); err != nil {
		t.Fatal(err)
	}
	calls, fail := 0, false
	old := HostExec
	HostExec = func(io.Writer, string, string, string, string, string, knowledge.Step) (int, error) {
		calls++
		if fail {
			return 1, errors.New("exited with status 1")
		}
		return 0, nil
	}
	defer func() { HostExec = old }()
	expect := func(want int, why string) {
		t.Helper()
		if calls != want {
			t.Fatalf("%s: %d runs, want %d", why, calls, want)
		}
	}
	if err := depsRun(t, root, instanceDir, base, plan); err != nil {
		t.Fatal(err)
	}
	expect(1, "first start")
	depsRun(t, root, instanceDir, base, plan)
	expect(1, "lockfile unchanged")
	write("yarn.lock", "v2")
	depsRun(t, root, instanceDir, base, plan)
	expect(2, "root lockfile changed")
	write("apps/api/package-lock.json", "{}")
	depsRun(t, root, instanceDir, base, plan)
	expect(3, "lockfile in the step dir changed")
	write("yarn.lock", "v3")
	fail = true
	if err := depsRun(t, root, instanceDir, base, plan); err == nil {
		t.Fatal("a failed install must fail the run")
	}
	expect(4, "changed again, fails")
	fail = false
	depsRun(t, root, instanceDir, base, plan)
	expect(5, "the failure stored no hash")
	depsRun(t, root, instanceDir, base, plan)
	expect(5, "unchanged after success")
}
