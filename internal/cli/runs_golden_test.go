package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// runSteps is a plan that exercises every runner path: prepare, a compose-up
// per mode, a schema step the seed prefix covers, the data seed and a host step.
var runSteps = []knowledge.Step{
	{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", ".agent-dev/app.compose.yaml"}},
	{ID: "up-all", Kind: knowledge.KindComposeUp, Modes: []string{"container"}},
	{ID: "up-deps", Kind: knowledge.KindComposeUp, Modes: []string{"hybrid"}, Exclude: []string{"web"}},
	{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "m"},
	{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "db", Cmd: "s"},
	{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "npm run dev", Port: 3000},
}

// normalizedRun reads runs/<id>.json of the only instance in stateDir with
// times, pid and the instance name replaced, so it can be compared to a golden file.
func normalizedRun(t *testing.T, stateDir string, id int, paths ...string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(stateDir, "*", "runs", fmt.Sprintf("%d.json", id)))
	if len(files) != 1 {
		t.Fatalf("run %d files: %v", id, files)
	}
	var m map[string]any
	if err := json.Unmarshal(mustRead(t, files[0]), &m); err != nil {
		t.Fatal(err)
	}
	project := m["project"].(string)
	delete(m, "recipe_hash")
	delete(m, "snapshot_hash")
	delete(m, "execution_proofs")
	m["project"], m["branch"], m["pid"] = "PROJECT", "BRANCH", 0
	for _, k := range []string{"started_at", "ended_at"} {
		if _, ok := m[k]; ok {
			m[k] = "TIME"
		}
	}
	for _, st := range m["steps"].([]any) {
		st := st.(map[string]any)
		delete(st, "definition_hash")
		delete(st, "executed_hash")
		for _, k := range []string{"started_at", "ended_at"} {
			if _, ok := st[k]; ok {
				st[k] = "TIME"
			}
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	out := string(b) + "\n"
	out = strings.ReplaceAll(out, project, "PROJECT")
	for _, p := range paths {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			out = strings.ReplaceAll(strings.ReplaceAll(out, jsonEsc(r), "ROOT"), r, "ROOT") // JSON doubles Windows backslashes
		}
	}
	return out
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("STACKFORK_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := string(mustRead(t, path)); got != want {
		t.Fatalf("%s differs\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestStartEnvRunGoldenContainer(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, runSteps)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1") // new volume: the seed runs and covers migrate
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	assertGolden(t, "run-start-env-container.json", normalizedRun(t, state, 1, repo))
	// second Start: nothing to prepare, data exists
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "0")
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	assertGolden(t, "run-start-env-container-again.json", normalizedRun(t, state, 2, repo))
}

func TestStartEnvRunGoldenHybrid(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, runSteps)
	old := lifecycle.HostStart
	lifecycle.HostStart = func(out, _ io.Writer, _ []string, _, _ string, s knowledge.Step) error {
		fmt.Fprintf(out, "started %s\n", s.Name)
		return nil
	}
	defer func() { lifecycle.HostStart = old }()
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "--root", repo, "plan", "--trust")...); code != 0 {
		t.Fatal(e)
	}
	if _, e, code := runCLI(t, "config", "set-mode", repo, "hybrid"); code != 0 {
		t.Fatal(e)
	}
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	assertGolden(t, "run-start-env-hybrid.json", normalizedRun(t, state, 1, repo))
}

// A failing step stops the run: later steps stay pending.
func TestStartEnvRunGoldenFailedSeed(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, runSteps)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 {
		t.Fatal("start-env must fail when the seed fails")
	}
	_ = e
	assertGolden(t, "run-start-env-failed-seed.json", normalizedRun(t, state, 1, repo))
}

// hostExecSteps is a database-only flow: the migrate runs on the host after the up.
var hostExecSteps = []knowledge.Step{
	{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", ".agent-dev/app.compose.yaml"}},
	{ID: "up", Kind: knowledge.KindComposeUp},
	{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Once: knowledge.OnceSchema, Cmd: "bun run db:migrate", Dir: "apps/api"},
	{ID: "after", Kind: knowledge.KindNote, Text: "open the app"},
}

// stubHostExec replaces the host runner; it records the steps it was given.
func stubHostExec(t *testing.T, code int) *[]string {
	t.Helper()
	var got []string
	old := lifecycle.HostExec
	lifecycle.HostExec = func(w io.Writer, _, _, _, _, _ string, s knowledge.Step) (int, error) {
		got = append(got, s.ID+": "+s.Cmd)
		fmt.Fprintf(w, "ran %s\n", s.Cmd)
		return code, nil
	}
	t.Cleanup(func() { lifecycle.HostExec = old })
	return &got
}

func TestStartEnvHostExecRunsOnTheHostInBothModes(t *testing.T) {
	for _, mode := range []string{"container", "hybrid"} {
		t.Run(mode, func(t *testing.T) {
			root, state := reposEnv(t)
			steps := append([]knowledge.Step{}, hostExecSteps...)
			if mode == "hybrid" { // hybrid needs a host step; it is the plan's own
				steps = append(steps, knowledge.Step{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "npm run dev", Port: 3000})
				old := lifecycle.HostStart
				lifecycle.HostStart = func(out, _ io.Writer, _ []string, _, _ string, s knowledge.Step) error { return nil }
				t.Cleanup(func() { lifecycle.HostStart = old })
			}
			repo := planRepo(t, root, steps)
			log := installFakeDocker(t)
			calls := stubHostExec(t, 0)
			base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
			runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
			runCLI(t, "config", "set-mode", repo, mode)
			if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
				t.Fatalf("%d %s %s", code, out, e)
			}
			if strings.Join(*calls, "|") != "migrate: bun run db:migrate" {
				t.Fatalf("host calls %v", *calls)
			}
			if b, _ := os.ReadFile(log); strings.Contains(string(b), " exec ") {
				t.Fatalf("a host step must not call docker compose exec:\n%s", b)
			}
			if mode == "container" {
				assertGolden(t, "run-start-env-host-exec.json", normalizedRun(t, state, 1, repo))
			}
		})
	}
}

// A failing host step stops the run; later steps stay pending.
func TestStartEnvHostExecFailureStopsTheRun(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hostExecSteps)
	stubHostExec(t, 3)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "migrate") {
		t.Fatalf("%d %s", code, e)
	}
	assertGolden(t, "run-start-env-host-exec-failed.json", normalizedRun(t, state, 1, repo))
}

func TestStartEnvHostExecUntrustedRefusesBeforeDocker(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hostExecSteps)
	log := installFakeDocker(t)
	calls := stubHostExec(t, 0)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "not trusted on this machine") || !strings.Contains(e, "stackfork plan --trust") || !strings.Contains(e, "on: host") {
		t.Fatalf("%d %s", code, e)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "compose") || len(*calls) != 0 {
		t.Fatalf("docker or the host ran:\n%s %v", b, *calls)
	}
}
