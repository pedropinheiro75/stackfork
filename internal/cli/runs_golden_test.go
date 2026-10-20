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
	m["project"], m["branch"], m["pid"] = "PROJECT", "BRANCH", 0
	for _, k := range []string{"started_at", "ended_at"} {
		if _, ok := m[k]; ok {
			m[k] = "TIME"
		}
	}
	for _, st := range m["steps"].([]any) {
		st := st.(map[string]any)
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
			out = strings.ReplaceAll(out, r, "ROOT")
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
