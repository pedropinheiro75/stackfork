package inventory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
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
	if i := collectOne(t, base); i.SeedError != "seeding\nerror: duplicate key\nerror: exited with status 1" || strings.Contains(i.SeedError, "old log") || i.SeedRun != 1 || i.SeedStep != "seed" {
		t.Fatalf("run log: %q", i.SeedError)
	}
	// a newer run that did not reach the seed does not hide it
	r2, _ := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli"})
	r2.Step("up", "compose-up", "up")
	r2.Finish(errors.New("failed"))
	if i := collectOne(t, base); i.SeedError != "seeding\nerror: duplicate key\nerror: exited with status 1" {
		t.Fatalf("older seed run: %q", i.SeedError)
	}
}

func TestLastRunCarriesRetryTailAndRunningStep(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-run-3", root, "main", []string{"web"}, nil)
	dir := filepath.Join(base, "sf-run-3")
	r, _ := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli", Mode: "container"})
	r.Step("prepare", "prepare", "prepare")
	r.Step("app", "host", "app")
	s := r.Step("prepare", "prepare", "prepare")
	s.Start()
	if lr := collectOne(t, base).LastRun; lr == nil || lr.Outcome != runs.Running || lr.Step != "prepare" || lr.StepN != 1 || lr.StepTotal != 2 || lr.Tail != "" {
		t.Fatalf("running: %+v", lr)
	}
	s.End(nil, 0)
	app := r.Step("app", "host", "app")
	w := app.Start()
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(w, "line %d\n\n", i)
	}
	app.End(errors.New("exited with status 2"), 2)
	r.SetRetry("stackfork --root /x start-env")
	r.Finish(errors.New("step app failed"))
	lr := collectOne(t, base).LastRun
	if lr.Outcome != runs.Failed || lr.FailedStep != "app" || lr.Retry != "stackfork --root /x start-env" || lr.Step != "" ||
		lr.Tail != "line 5\nline 6\nline 7\nline 8\nline 9\nerror: exited with status 2" {
		t.Fatalf("failed: %+v", lr)
	}
}

// The seed step is found as SeedEntry finds it: through the main checkout's
// plan when the instance is a linked worktree.
func TestSeedRunLogUsesTheMainCheckoutsPlan(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	main := gitRepo(t, "main")
	wt := filepath.Join(main, ".worktrees", "wt")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(main, ".git", "worktrees", "wt")+"\n"), 0o644)
	os.WriteFile(filepath.Join(wt, "package.json"), []byte(`{"scripts":{"db:seed":"bun scripts/seed"}}`), 0o600)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "load-data", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "bun scripts/seed"},
	}}}
	if err := k.Save(filepath.Join(main, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	writeInstance(t, base, "sf-seed-3", wt, "wt", []string{"web"}, nil)
	dir := filepath.Join(base, "sf-seed-3")
	mp := filepath.Join(dir, "manifest.json")
	m, _ := state.ReadManifest(mp)
	f := false
	m.Seeded = &f
	state.WritePrivate(mp, m)
	r, _ := runs.Begin(dir, runs.Meta{Command: "start-env", Trigger: "cli"})
	s := r.Step("load-data", "exec", "seed")
	s.Start().Write([]byte("seed failed here\n"))
	s.End(errors.New("exited with status 1"), 1)
	r.Finish(errors.New("failed"))
	if i := collectOne(t, base); i.SeedStep != "load-data" || i.SeedError != "seed failed here\nerror: exited with status 1" {
		t.Fatalf("%q %q", i.SeedStep, i.SeedError)
	}
}
