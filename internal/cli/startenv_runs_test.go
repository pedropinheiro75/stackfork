package cli

import (
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func runFiles(stateDir string) []string {
	files, _ := filepath.Glob(filepath.Join(stateDir, "*", "runs", "*.json"))
	return files
}

func TestStartEnvRefusalsWriteNoRun(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hybridSteps)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, "config", "set-mode", repo, "hybrid")
	if _, e, code := runCLI(t, append(base, "start-env", repo)...); code == 0 || !strings.Contains(e, "not trusted on this machine") {
		t.Fatalf("%d %s", code, e)
	}
	if dirs, _ := filepath.Glob(filepath.Join(state, "*", "runs")); len(dirs) != 0 || len(runFiles(state)) != 0 {
		t.Fatalf("a refused Start wrote %v", dirs)
	}
	// a project that is not ready is a refusal too
	bare := scratchRepo(t, filepath.Join(root, "bare"))
	if _, _, code := runCLI(t, append(base, "start-env", bare)...); code == 0 {
		t.Fatal("start-env of an unlearned project must fail")
	}
	if dirs, _ := filepath.Glob(filepath.Join(state, "*", "runs")); len(dirs) != 0 {
		t.Fatalf("a not-ready Start wrote %v", dirs)
	}
}

func TestStartEnvRecordsExactlyOneRun(t *testing.T) {
	root, state := reposEnv(t)
	repo := readyRepo(t, root)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if files := runFiles(state); len(files) != 1 || filepath.Base(files[0]) != "1.json" {
		t.Fatalf("runs: %v", files)
	}
}

func TestStartEnvRefusesWhileAnotherRunIsActive(t *testing.T) {
	root, state := reposEnv(t)
	repo := readyRepo(t, root)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatal(e)
	}
	files := runFiles(state)
	dir := filepath.Dir(filepath.Dir(files[0]))
	held, err := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Finish(nil)
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "another run is in progress") {
		t.Fatalf("%d %s", code, e)
	}
	if got := runFiles(state); len(got) != 2 { // the held run is the second
		t.Fatalf("runs: %v", got)
	}
}

func schemaRunStates(t *testing.T, migrateService string) (states, seedText string) {
	t.Helper()
	root, state := reposEnv(t)
	repo := planRepo(t, root, []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: migrateService, Cmd: "m"},
		{ID: "migrate2", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "m2"},
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "db", Cmd: "s"},
	})
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	r, err := runs.Get(onlyRunDir(t, state), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Steps {
		if s.ID == "seed" {
			seedText = s.Text
		}
	}
	return stepStates(r), seedText
}

// The seed's migrate prefix covers the first schema step only, and only when
// both run in the same service.
func TestOnlyThePrefixedSchemaStepIsSkipped(t *testing.T) {
	got, text := schemaRunStates(t, "db")
	want := "prepare=succeeded: up=succeeded: seed=succeeded: migrate=skipped:ran with the seed migrate2=succeeded:"
	if got != want || text != "m && s  in db" {
		t.Fatalf("%s\n%q", got, text)
	}
}

func TestSchemaStepInAnotherServiceIsNotPrefixedAndRunsAtItsPlace(t *testing.T) {
	got, text := schemaRunStates(t, "web")
	want := "prepare=succeeded: up=succeeded: seed=succeeded: migrate=succeeded: migrate2=succeeded:"
	if got != want || text != "s  in db" {
		t.Fatalf("%s\n%q", got, text)
	}
}
