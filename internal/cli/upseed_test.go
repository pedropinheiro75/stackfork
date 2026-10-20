package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// upSeedRun runs UpAndSeed on the prepared seed fixture and returns the run.
func upSeedRun(t *testing.T, root, stateDir string) (*runs.Run, lifecycle.UpOutcome, string) {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(stateDir, "*", "manifest.json"))
	instanceDir := filepath.Dir(dirs[0])
	project := filepath.Base(instanceDir)
	root, _ = identity.ResolvePath(root)
	branch, _ := currentBranch(root)
	var out bytes.Buffer
	run, err := runs.Begin(instanceDir, runs.Meta{Command: "up", Trigger: "cli", Mode: "container", Project: project, Branch: branch, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	main := repos.MainRepoOf(root)
	res := lifecycle.UpAndSeed(&out, &out, lifecycle.UpSeed{
		Root: root, Main: main, Branch: branch, Project: project, InstanceDir: instanceDir,
		NewBase: stateDir, Mode: "container", Plan: lifecycle.LoadPlan(root, main), Run: run, Seed: true,
		Compose: func(so, se io.Writer) (int, error) {
			return lifecycle.Execute(so, se, "up", root, branch, project, instanceDir, stateDir, "", lifecycle.Options{})
		},
	})
	run.Finish(res.Err)
	got, err := runs.Get(instanceDir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got, res, instanceDir
}

func stepStates(r *runs.Run) string {
	var l []string
	for _, s := range r.Steps {
		l = append(l, s.ID+"="+s.State+":"+s.Reason)
	}
	return strings.Join(l, " ")
}

func TestUpAndSeedRecordsNewVolumeSeed(t *testing.T) {
	root, stateDir, log := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	r, res, dir := upSeedRun(t, root, stateDir)
	if got := stepStates(r); got != "up=succeeded: seed=succeeded:" || res.SeedErr != nil {
		t.Fatalf("%s %v", got, res.SeedErr)
	}
	if seedExecs(t, log) != 1 || seededMarker(t, stateDir) != "true" {
		t.Fatal("seed did not run once")
	}
	b, err := runs.Log(dir, r.ID, "seed")
	if err != nil || !strings.Contains(string(b), "seeding the new database") {
		t.Fatalf("%q %v", b, err)
	}
	if logs, _ := filepath.Glob(filepath.Join(dir, "seed.log")); len(logs) != 0 {
		t.Fatalf("seed.log written: %v", logs)
	}
}

func TestUpAndSeedSkipsWhenDataExists(t *testing.T) {
	root, stateDir, log := seedFixture(t) // fake volume exists
	r, _, _ := upSeedRun(t, root, stateDir)
	if got := stepStates(r); got != "up=succeeded: seed=skipped:data already exists" {
		t.Fatal(got)
	}
	if seedExecs(t, log) != 0 || seededMarker(t, stateDir) != "true" {
		t.Fatal("seed ran or marker changed")
	}
}

func TestUpAndSeedFailedSeed(t *testing.T) {
	root, stateDir, _ := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	r, res, _ := upSeedRun(t, root, stateDir)
	if got := stepStates(r); got != "up=succeeded: seed=failed:" || res.SeedErr == nil || res.Err != nil || res.Code != 0 {
		t.Fatalf("%s %+v", got, res)
	}
	if code := r.Steps[1].ExitCode; code == nil || *code != 1 {
		t.Fatalf("exit code %v", code)
	}
	if seededMarker(t, stateDir) != "false" {
		t.Fatal("marker must stay false")
	}
}
