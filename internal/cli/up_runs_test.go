package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func onlyRunDir(t *testing.T, stateDir string) string {
	t.Helper()
	ms, _ := filepath.Glob(filepath.Join(stateDir, "*", "manifest.json"))
	if len(ms) != 1 {
		t.Fatalf("instances: %v", ms)
	}
	return filepath.Dir(ms[0])
}

func TestUpRecordsItsOwnRun(t *testing.T) {
	root, stateDir, _ := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	args := []string{"--root", root, "--state-dir", stateDir}
	if out, e, code := runCLI(t, append(args, "up")...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	dir := onlyRunDir(t, stateDir)
	r, err := runs.Get(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Command != "up" || r.Trigger != "cli" || r.Outcome != runs.Succeeded || stepStates(r) != "up=succeeded: seed=succeeded:" {
		t.Fatalf("%s %s %s %s", r.Command, r.Trigger, r.Outcome, stepStates(r))
	}
	if b, err := runs.Log(dir, 1, "seed"); err != nil || !strings.Contains(string(b), "seeding the new database") {
		t.Fatalf("%q %v", b, err)
	}
}

func TestUpInRunRecordsAndSeedsNothing(t *testing.T) {
	root, stateDir, log := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	if out, e, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--in-run"); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if files := runFiles(stateDir); len(files) != 0 {
		t.Fatalf("runs: %v", files)
	}
	if seedExecs(t, log) != 0 || seededMarker(t, stateDir) != "nil" {
		t.Fatalf("--in-run seeded: %d %s", seedExecs(t, log), seededMarker(t, stateDir))
	}
	if out, _, _ := runCLI(t, "up", "--help"); strings.Contains(out, "in-run") {
		t.Fatalf("--in-run is listed in help:\n%s", out)
	}
}

func TestFailedUpIsAFailedRun(t *testing.T) {
	root, stateDir, _ := seedFixture(t)
	t.Setenv("DOCKER_FAKE_EXIT", "1")
	runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	r, err := runs.Get(onlyRunDir(t, stateDir), 1)
	if err != nil || r.Outcome != runs.Failed || !strings.Contains(r.Retry, " up") || stepStates(r) != "up=failed: seed=pending:" {
		t.Fatalf("%+v %v %s", r, err, stepStates(r))
	}
}

func TestSeedRecordsItsOwnRun(t *testing.T) {
	root, stateDir, _ := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	args := []string{"--root", root, "--state-dir", stateDir}
	runCLI(t, append(args, "up")...) // run 1: the seed fails
	r, _ := runs.Get(onlyRunDir(t, stateDir), 1)
	if r.Outcome != runs.Failed || !strings.HasSuffix(r.Retry, " seed") || stepStates(r) != "up=succeeded: seed=failed:" {
		t.Fatalf("%s %q %s", r.Outcome, r.Retry, stepStates(r))
	}
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "0")
	if _, e, code := runCLI(t, append(args, "seed")...); code != 0 {
		t.Fatal(e)
	}
	r, _ = runs.Get(onlyRunDir(t, stateDir), 2)
	if r.Command != "seed" || r.Outcome != runs.Succeeded || stepStates(r) != "seed=succeeded:" {
		t.Fatalf("%s %s %s", r.Command, r.Outcome, stepStates(r))
	}
}

func TestSeedMarkDoneAndNothingToRunWriteNoRun(t *testing.T) {
	root, stateDir, _ := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	args := []string{"--root", root, "--state-dir", stateDir}
	runCLI(t, append(args, "up")...)
	if _, e, code := runCLI(t, append(args, "seed", "--mark-done")...); code != 0 {
		t.Fatal(e)
	}
	runCLI(t, append(args, "seed")...) // already seeded: refused
	if files := runFiles(stateDir); len(files) != 1 {
		t.Fatalf("runs: %v", files)
	}
}

// The dashboard's /api/run children carry STACKFORK_TRIGGER=dashboard.
func TestUpAndSeedTriggerFromEnvironment(t *testing.T) {
	root, stateDir, _ := seedFixture(t)
	t.Setenv("STACKFORK_TRIGGER", "dashboard")
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	args := []string{"--root", root, "--state-dir", stateDir}
	runCLI(t, append(args, "up")...)
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "0")
	runCLI(t, append(args, "seed")...)
	for id := 1; id <= 2; id++ {
		if r, err := runs.Get(onlyRunDir(t, stateDir), id); err != nil || r.Trigger != "dashboard" {
			t.Fatalf("run %d: %+v %v", id, r, err)
		}
	}
	t.Setenv("STACKFORK_TRIGGER", "anything else")
	runCLI(t, append(args, "up")...)
	if r, _ := runs.Get(onlyRunDir(t, stateDir), 3); r.Trigger != "cli" {
		t.Fatalf("%s", r.Trigger)
	}
}
