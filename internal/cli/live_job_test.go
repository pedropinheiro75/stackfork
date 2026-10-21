package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	state_ "github.com/pedropinheiro75/stackfork/internal/state"
)

func lc(path, cat, svc string) live.Change {
	return live.Change{Path: path, Category: cat, Service: svc}
}

// liveFixture starts the instance once (run 1, a plain Start) and returns what
// a live run needs.
func liveFixture(t *testing.T, steps []knowledge.Step) (repo, state, log string, opts StartOptions) {
	t.Helper()
	root, state := reposEnv(t)
	repo = planRepo(t, root, steps)
	log = installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "0") // the data exists: only a live up that created it seeds
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	return repo, state, log, StartOptions{Checkout: repo, NewStateBase: state, LegacyStateBase: t.TempDir(), Mode: knowledge.ModeContainer}
}

// dockerAfter is what the fake docker logged after offset.
func dockerAfter(t *testing.T, log string, offset int) string {
	t.Helper()
	b, _ := os.ReadFile(log)
	return string(b[offset:])
}

func fileSize(log string) int {
	b, _ := os.ReadFile(log)
	return len(b)
}

// forbidden are the Compose arguments a live run never passes (design D6).
var forbidden = []string{"down", "restart", "--volumes", "-V", "--renew-anon-volumes", "--remove-orphans", "--force-recreate", "prune"}

func assertNoForbiddenArgs(t *testing.T, dockerLog string) {
	t.Helper()
	for _, line := range strings.Split(dockerLog, "\n") {
		for _, tok := range strings.Fields(line) {
			for _, f := range forbidden {
				if tok == f {
					t.Errorf("a live run passed %q: %s", f, line)
				}
			}
		}
	}
}

func runLive(t *testing.T, opts StartOptions, job live.Job) (string, error) {
	t.Helper()
	opts.Live = &job
	var out bytes.Buffer
	err := StartEnv(&out, &out, opts)
	return out.String(), err
}

func allStarted(string) bool { return true }

func TestLiveRunGoldenBuild(t *testing.T) {
	repo, state, log, opts := liveFixture(t, runSteps)
	off := fileSize(log)
	job := live.BuildJob(knowledge.Plan{Steps: runSteps}, knowledge.ModeContainer, []live.Change{lc("bun.lock", live.CatBuild, "web")}, allStarted)
	if out, err := runLive(t, opts, job); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertGolden(t, "run-live-build.json", normalizedRun(t, state, 2, repo))
	d := dockerAfter(t, log, off)
	if strings.Count(d, " up -d ") != 1 || !strings.Contains(d, "--build web") || strings.Contains(d, " config ") {
		t.Errorf("a lockfile change is one build of web, no prepare:\n%s", d)
	}
	assertNoForbiddenArgs(t, d)
}

func TestLiveRunGoldenCombined(t *testing.T) {
	repo, state, log, opts := liveFixture(t, runSteps)
	off := fileSize(log)
	changes := []live.Change{lc("docker-compose.yml", live.CatCompose, ""), lc("bun.lock", live.CatBuild, "web"), lc("db/migrations/2/m.sql", live.CatMigrations, "")}
	job := live.BuildJob(knowledge.Plan{Steps: runSteps}, knowledge.ModeContainer, changes, allStarted)
	// the compose file is a manifest input: touching it makes prepare run
	if err := os.WriteFile(repo+"/.agent-dev/app.compose.yaml", []byte(planCompose+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runLive(t, opts, job); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertGolden(t, "run-live-combined.json", normalizedRun(t, state, 2, repo))
	d := dockerAfter(t, log, off)
	if strings.Count(d, " up -d ") != 1 || !strings.Contains(d, " --build") {
		t.Errorf("compose and build join one up --build:\n%s", d)
	}
	if !strings.Contains(d, " exec ") || !strings.Contains(d, "sh -c m") {
		t.Errorf("the schema step must run:\n%s", d)
	}
	assertNoForbiddenArgs(t, d)
}

func TestLiveRunGoldenMigrations(t *testing.T) {
	repo, state, log, opts := liveFixture(t, runSteps)
	off := fileSize(log)
	job := live.BuildJob(knowledge.Plan{Steps: runSteps}, knowledge.ModeContainer, []live.Change{lc("db/migrations/2/m.sql", live.CatMigrations, "")}, allStarted)
	if out, err := runLive(t, opts, job); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertGolden(t, "run-live-migrations.json", normalizedRun(t, state, 2, repo))
	d := dockerAfter(t, log, off)
	if strings.Contains(d, " up ") || !strings.Contains(d, " exec ") {
		t.Errorf("migrations alone are an exec, no up:\n%s", d)
	}
	assertNoForbiddenArgs(t, d)
}

// A live up that finds no data volume seeds, as a Start does (UpAndSeed's
// rule); one that finds the data does not.
func TestLiveRunSeedKeepsItsRule(t *testing.T) {
	for _, tc := range []struct{ volumeExit, want string }{{"1", "seed=succeeded"}, {"0", "seed=skipped:data already exists"}} {
		t.Run("volume exit "+tc.volumeExit, func(t *testing.T) {
			repo, state, _, opts := liveFixture(t, runSteps)
			t.Setenv("DOCKER_FAKE_VOLUME_EXIT", tc.volumeExit)
			os.WriteFile(repo+"/.agent-dev/app.compose.yaml", []byte(planCompose+"# edited\n"), 0o644) // prepare runs again
			mf := filepath.Join(onlyRunDir(t, state), "manifest.json")
			m, _ := state_.ReadManifest(mf)
			m.Seeded = nil // the first Start decided; a new database leaves the decision open
			if err := state_.WritePrivate(mf, m); err != nil {
				t.Fatal(err)
			}
			job := live.BuildJob(knowledge.Plan{Steps: runSteps}, knowledge.ModeContainer, []live.Change{lc("docker-compose.yml", live.CatCompose, "")}, allStarted)
			if out, err := runLive(t, opts, job); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			r, _ := runs.Get(onlyRunDir(t, state), 2)
			if got := stepStates(r); !strings.Contains(got, tc.want) {
				t.Fatalf("%s", got)
			}
		})
	}
}

func TestLiveRunNeverRunsHostSteps(t *testing.T) {
	_, _, _, opts := liveFixture(t, runSteps)
	opts.Mode = knowledge.ModeHybrid
	started := func(s string) bool { return s != "web" }
	job := live.BuildJob(knowledge.Plan{Steps: runSteps}, knowledge.ModeHybrid, []live.Change{lc("docker-compose.yml", live.CatCompose, "")}, started)
	called := false
	old := lifecycle.HostStart
	lifecycle.HostStart = func(io.Writer, io.Writer, []string, string, string, knowledge.Step) error { called = true; return nil }
	defer func() { lifecycle.HostStart = old }()
	base := []string{"--state-dir", opts.NewStateBase, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "--root", opts.Checkout, "plan", "--trust")...); code != 0 {
		t.Fatal(e)
	}
	if out, err := runLive(t, opts, job); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if called {
		t.Fatal("a live run started a host step")
	}
}
