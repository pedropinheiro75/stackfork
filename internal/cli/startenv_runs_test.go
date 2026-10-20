package cli

import (
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
