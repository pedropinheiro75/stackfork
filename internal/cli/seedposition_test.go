package cli

import (
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// fieldPlan is the field report: install and build run on the host between the
// compose-up and the data step, the seed is prefixed by the migrate.
func fieldPlan() []knowledge.Step {
	return hostSeedPlan(
		hostStep("install", "", "yarn install", ""),
		hostStep("build-packages", "", "yarn build:packages", ""),
		hostStep("migrate", knowledge.OnceSchema, "yarn db:migrate:prod", ""),
		hostStep("seed", knowledge.OnceData, "yarn db:seed:prod:testdata", ""),
		knowledge.Step{ID: "login", Kind: knowledge.KindNote, Text: "log in"})
}

func TestSeedRunsAtItsRecordedPosition(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, e, states, _, _ := startHostSeed(t, fieldPlan())
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	want := "install: yarn install|build-packages: yarn build:packages|migrate: yarn db:migrate:prod|seed: yarn db:seed:prod:testdata"
	if got := strings.Join(*calls, "|"); got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}
	if !strings.Contains(states, "migrate=skipped:ran with the seed") || strings.Index(states, "build-packages=") > strings.Index(states, "seed=") {
		t.Fatal(states)
	}
}

func TestSeedAtItsPositionSkipsWhenDataExists(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "0")
	code, e, states, _, _ := startHostSeed(t, fieldPlan())
	if code != 0 || !strings.Contains(states, "seed=skipped:data already exists") {
		t.Fatalf("%d %s %s", code, e, states)
	}
	if strings.Contains(strings.Join(*calls, "|"), "seed:") || strings.Contains(states, "ran with the seed") {
		t.Fatalf("%v %s", *calls, states)
	}
}

// A data step right after the compose-up keeps today's order.
func TestSeedRightAfterUpKeepsItsOrder(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, e, states, _, _ := startHostSeed(t, hostSeedPlan(
		hostStep("seed", knowledge.OnceData, "bun run db:seed", ""),
		hostStep("install", "", "yarn install", "")))
	if code != 0 || strings.Join(*calls, "|") != "seed: bun run db:seed|install: yarn install" {
		t.Fatalf("%d %s %v %s", code, e, *calls, states)
	}
}

func TestPlanTextShowsTheSeedWhereItRuns(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, fieldPlan())
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "--root", repo, "plan")...)
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	last := -1
	for _, id := range []string{"prepare", "up", "install", "build-packages", "migrate", "seed", "login"} {
		i := strings.Index(out, "\n  "+id+" [")
		if i < last {
			t.Fatalf("%s out of order:\n%s", id, out)
		}
		last = i
	}
	t.Log(out)
}
