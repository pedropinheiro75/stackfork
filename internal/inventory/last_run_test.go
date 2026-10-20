package inventory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestLastRunIsTheNewestRun(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-run-1", root, "main", []string{"web"}, nil)
	dir := filepath.Join(base, "sf-run-1")
	if i := collectOne(t, base); i.LastRun != nil {
		t.Fatalf("no runs, no last_run: %+v", i.LastRun)
	}
	r1, _ := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli", Mode: "container"})
	r1.Step("up", "compose-up", "compose up").Start()
	r1.Finish(nil)
	r2, _ := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "dashboard", Mode: "container"})
	up := r2.Step("up", "compose-up", "compose up")
	up.Start()
	up.End(errors.New("boom"), 1)
	r2.Step("seed", "exec", "seed")
	r2.Finish(errors.New("up exited"))
	lr := collectOne(t, base).LastRun
	if lr == nil || lr.ID != 2 || lr.Command != "up" || lr.Trigger != "dashboard" || lr.Outcome != runs.Failed || lr.FailedStep != "up" || lr.StartedAt.IsZero() || lr.EndedAt.IsZero() {
		t.Fatalf("%+v", lr)
	}
}

func TestSeedErrorReadsTheSeedStepLogThenSeedLog(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"db:seed":"bun scripts/seed"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeInstance(t, base, "sf-seed-2", root, "main", []string{"web"}, nil)
	dir := filepath.Join(base, "sf-seed-2")
	mp := filepath.Join(dir, "manifest.json")
	m, _ := state.ReadManifest(mp)
	f := false
	m.Seeded = &f
	state.WritePrivate(mp, m)
	// an instance seeded before runs existed: seed.log is the fallback
	os.WriteFile(filepath.Join(dir, state.SeedLog), []byte("old log\n"), 0o600)
	if i := collectOne(t, base); i.SeedError != "old log" {
		t.Fatalf("fallback: %q", i.SeedError)
	}
	// a run that ran the seed wins
	r, _ := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli"})
	r.Step("up", "compose-up", "up")
	s := r.Step("seed", "exec", "seed")
	w := s.Start()
	w.Write([]byte("seeding\nerror: duplicate key\n"))
	s.End(errors.New("exited with status 1"), 1)
	r.Finish(errors.New("failed"))
	if i := collectOne(t, base); i.SeedError != "seeding\nerror: duplicate key" || strings.Contains(i.SeedError, "old log") {
		t.Fatalf("run log: %q", i.SeedError)
	}
	// a newer run that did not reach the seed does not hide it
	r2, _ := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli"})
	r2.Step("up", "compose-up", "up")
	r2.Finish(errors.New("failed"))
	if i := collectOne(t, base); i.SeedError != "seeding\nerror: duplicate key" {
		t.Fatalf("older seed run: %q", i.SeedError)
	}
}
