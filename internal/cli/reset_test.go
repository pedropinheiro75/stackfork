package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

var resetSteps = []knowledge.Step{
	{ID: "up", Kind: knowledge.KindComposeUp},
	{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "m"},
	{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "db", Cmd: "s"},
}

// resetFixture starts a planned repo once, so a reset has an environment to throw away.
func resetFixture(t *testing.T) (repo, state string, base []string, log string) {
	t.Helper()
	root, state := reposEnv(t)
	repo = planRepo(t, root, resetSteps)
	log = installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1") // no data volume: the seed runs
	base = []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	return repo, state, base, log
}

func dockerLog(log string) string { b, _ := os.ReadFile(log); return string(b) }

// destructive counts the docker calls that remove or recreate something.
func destructive(log string) int {
	l := dockerLog(log)
	return countLines(l, " down ") + countLines(l, " up -d ")
}

func TestResetRecordsTheStepsInOrder(t *testing.T) {
	repo, state, base, log := resetFixture(t)
	before := dockerLog(log)
	out, e, code := runCLI(t, append(base, "--root", repo, "reset", "--yes")...)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	r, err := runs.Get(onlyRunDir(t, state), 2)
	if err != nil || r.Command != "reset" || r.Outcome != runs.Succeeded {
		t.Fatalf("%+v %v", r, err)
	}
	want := "sync=succeeded: secrets=skipped:no secrets down=succeeded: prepare=succeeded: up=succeeded: seed=succeeded: migrate=skipped:ran with the seed"
	if got := stepStates(r); got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}
	if r.Retry != "" {
		t.Fatalf("retry on success: %q", r.Retry)
	}
	l := strings.TrimPrefix(dockerLog(log), before)
	d, u := strings.Index(l, " down "), strings.Index(l, " up -d ")
	if d < 0 || u < d || !strings.Contains(l[d:u], "--volumes") || !strings.Contains(l[u:], "--build") {
		t.Fatalf("docker calls:\n%s", l)
	}
	if countLines(l, " config ") != 1 {
		t.Fatalf("prepare must re-render once:\n%s", l)
	}
	if !strings.Contains(out, "started") {
		t.Fatalf("no progress: %s", out)
	}
}

func TestResetIsRetriedByReset(t *testing.T) {
	repo, state, base, _ := resetFixture(t)
	t.Setenv("DOCKER_FAKE_EXIT", "1") // docker compose fails: down fails
	if _, _, code := runCLI(t, append(base, "--root", repo, "reset", "--yes")...); code == 0 {
		t.Fatal("a failing down must fail the reset")
	}
	r, _ := runs.Get(onlyRunDir(t, state), 2)
	if r.Outcome != runs.Failed || !strings.HasSuffix(r.Retry, " reset --yes") {
		t.Fatalf("%+v", r)
	}
}

func TestResetWithoutYesNeedsATerminal(t *testing.T) {
	repo, state, base, log := resetFixture(t)
	before := destructive(log)
	_, e, code := runCLI(t, append(base, "--root", repo, "reset")...)
	if code == 0 || !strings.Contains(e, "--yes") {
		t.Fatalf("%d %s", code, e)
	}
	if destructive(log) != before || len(runFiles(state)) != 1 {
		t.Fatal("a refused reset must change nothing")
	}
}

func askReset(t *testing.T, answers string, args ...string) (out, errOut string, code int) {
	t.Helper()
	oldIn, oldTTY := resetStdin, resetIsTTY
	resetStdin, resetIsTTY = strings.NewReader(answers), func() bool { return true }
	defer func() { resetStdin, resetIsTTY = oldIn, oldTTY }()
	return runCLI(t, args...)
}

func TestResetAsksAndListsTheVolumes(t *testing.T) {
	repo, state, base, log := resetFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUMES", "sf-db-data\nsf-cache\n")
	before := destructive(log)
	out, _, code := askReset(t, "n\n", append(base, "--root", repo, "reset")...)
	if code == 0 || !strings.Contains(out, "sf-db-data") || !strings.Contains(out, "sf-cache") || !strings.Contains(out, "[y/N]") {
		t.Fatalf("%d %s", code, out)
	}
	if destructive(log) != before || len(runFiles(state)) != 1 {
		t.Fatal("answering no must change nothing")
	}
	if _, e, code := askReset(t, "y\n", append(base, "--root", repo, "reset")...); code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	if r, err := runs.Get(onlyRunDir(t, state), 2); err != nil || r.Command != "reset" || r.Outcome != runs.Succeeded {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestResetByInstanceName(t *testing.T) {
	_, state, base, _ := resetFixture(t)
	name := filepath.Base(onlyRunDir(t, state))
	if out, e, code := runCLI(t, append(base, "--instance", name, "reset", "--yes")...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
}

// reset keeps the recorded recipe of a project nothing else knows, and the old seed marker does not stop the seed.
func TestResetKeepsThePrepareRecipe(t *testing.T) {
	root, state := reposEnv(t)
	repo := adaptedRepo(t, root) // no knowledge file: the recipe is the adapter file
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml")...); code != 0 {
		t.Fatal(e)
	}
	if out, e, code := runCLI(t, append(base, "--root", repo, "reset", "--yes")...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	l := dockerLog(log)
	if countLines(l, " config ") != 2 || countLines(l, " up -d ") != 1 {
		t.Fatalf("%s", l)
	}
}

// behindAndDirty gives repo an upstream one commit ahead and a local edit.
func behindAndDirty(t *testing.T, repo string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	other := filepath.Join(t.TempDir(), "other")
	for _, c := range [][]string{
		{"init", "-q", "--bare", "-b", "main", origin},
		{"-C", repo, "remote", "add", "origin", origin},
		{"-C", repo, "push", "-q", "-u", "origin", "main"},
		{"clone", "-q", origin, other},
		{"-C", other, "-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "up"},
		{"-C", other, "push", "-q", "origin", "main"},
	} {
		if out, err := exec.Command("git", c...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", c, out)
		}
	}
	os.WriteFile(filepath.Join(repo, "f"), []byte("local edit"), 0o644)
}

func TestResetBlockedSyncAsksAndNeverTouchesTheCode(t *testing.T) {
	repo, state, base, log := resetFixture(t)
	behindAndDirty(t, repo)
	head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	before := destructive(log)
	args := append(base, "--root", repo, "reset")
	out, _, code := askReset(t, "y\nn\n", args...)
	if code == 0 || !strings.Contains(out, "reset with the current code?") {
		t.Fatalf("%d %s", code, out)
	}
	if destructive(log) != before || len(runFiles(state)) != 1 {
		t.Fatal("declining the current code must change nothing")
	}
	if out, e, code := askReset(t, "y\ny\n", args...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if got, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output(); string(got) != string(head) {
		t.Fatal("HEAD moved")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "f")); string(b) != "local edit" {
		t.Fatal("local edit lost")
	}
}

// Inside the run a blocked sync stops before down unless the caller chose to continue.
func TestResetRunStopsBeforeDownWhenSyncIsBlocked(t *testing.T) {
	repo, state, base, log := resetFixture(t)
	behindAndDirty(t, repo)
	before := destructive(log)
	o := StartOptions{Checkout: repo, NewStateBase: state, LegacyStateBase: base[3], Reset: true, Trigger: "dashboard"}
	err := StartEnv(io.Discard, io.Discard, o)
	if err == nil || !strings.Contains(err.Error(), "current code") {
		t.Fatalf("%v", err)
	}
	if destructive(log) != before {
		t.Fatal("docker was called")
	}
	r, _ := runs.Get(onlyRunDir(t, state), 2)
	if r.Outcome != runs.Failed || !strings.Contains(stepStates(r), "sync=failed") {
		t.Fatalf("%+v", r)
	}
	o.Current = true
	if err := StartEnv(io.Discard, io.Discard, o); err != nil {
		t.Fatal(err)
	}
}

// A reset prepares with what the instance was last prepared with (its files
// and env file), even when knowledge carries a different recipe.
func TestResetPreparesWithTheManifestRecipe(t *testing.T) {
	dir := t.TempDir()
	env := "app.env"
	m := &state.Manifest{Files: []string{"/r/compose.yaml", "/r/override.yaml"}, EnvFile: &env}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded,
		Steps: []knowledge.Step{{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", "other.yaml"}}, {ID: "up", Kind: knowledge.KindComposeUp}}}}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	args, err := resetPrepareArgs(root, root, dir)
	want := "-f /r/compose.yaml -f /r/override.yaml --env-file app.env"
	if err != nil || strings.Join(args, " ") != want {
		t.Fatalf("got %v %v, want %s", args, err, want)
	}
}
