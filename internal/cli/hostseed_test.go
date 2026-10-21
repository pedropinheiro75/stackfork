package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// stubHostExecCodes is stubHostExec with an exit status per step id.
func stubHostExecCodes(t *testing.T, codes map[string]int) *[]string {
	t.Helper()
	var got []string
	old := lifecycle.HostExec
	lifecycle.HostExec = func(w io.Writer, _, _, _, _, _ string, s knowledge.Step) (int, error) {
		got = append(got, s.ID+": "+s.Cmd)
		fmt.Fprintf(w, "ran %s\n", s.Cmd)
		return codes[s.ID], nil
	}
	t.Cleanup(func() { lifecycle.HostExec = old })
	return &got
}

func hostStep(id, once, cmd, dir string) knowledge.Step {
	return knowledge.Step{ID: id, Kind: knowledge.KindExec, On: knowledge.OnHost, Once: once, Cmd: cmd, Dir: dir}
}

// startHostSeed trusts the plan and runs start-env; it returns the exit code,
// the run's steps and the docker log.
func startHostSeed(t *testing.T, steps []knowledge.Step) (code int, stderr, states, dockerLog, state string) {
	t.Helper()
	root, state := reposEnv(t)
	repo := planRepo(t, root, steps)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	_, stderr, code = runCLI(t, append(base, "start-env", repo)...)
	r, err := runs.Get(onlyRunDir(t, state), 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	return code, stderr, stepStates(r), string(b), state
}

func hostSeedPlan(extra ...knowledge.Step) []knowledge.Step {
	steps := []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", ".agent-dev/app.compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	}
	return append(steps, extra...)
}

func TestHostSeedRunsOnANewVolume(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, e, states, dlog, state := startHostSeed(t, hostSeedPlan(hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
	if code != 0 || strings.Join(*calls, "|") != "seed: bun run db:seed" {
		t.Fatalf("%d %s %v", code, e, *calls)
	}
	if !strings.Contains(states, "seed=succeeded:") || strings.Contains(dlog, " exec ") || seededMarker(t, state) != "true" {
		t.Fatalf("%s\n%s", states, dlog)
	}
}

func TestHostSeedSkipsWhenDataExists(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "0")
	code, e, states, _, _ := startHostSeed(t, hostSeedPlan(hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
	if code != 0 || len(*calls) != 0 || !strings.Contains(states, "seed=skipped:data already exists") {
		t.Fatalf("%d %s %v %s", code, e, *calls, states)
	}
}

func TestHostSeedFailureLeavesItUnseededWithARetry(t *testing.T) {
	stubHostExecCodes(t, map[string]int{"seed": 3})
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, e, states, _, state := startHostSeed(t, hostSeedPlan(hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
	if code == 0 || !strings.Contains(e, "retry with stackfork seed") || !strings.Contains(states, "seed=failed:") || seededMarker(t, state) != "false" {
		t.Fatalf("%d %s %s %s", code, e, states, seededMarker(t, state))
	}
}

// The migrate prefix: a host migrate with the seed's dir runs first, as its
// own argument vector, and the schema step is skipped.
func TestHostMigratePrefixesAHostSeed(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, e, states, _, _ := startHostSeed(t, hostSeedPlan(
		hostStep("migrate", knowledge.OnceSchema, "bun run db:migrate", "apps/api"),
		hostStep("seed", knowledge.OnceData, "bun run db:seed", "apps/api")))
	if code != 0 || strings.Join(*calls, "|") != "migrate: bun run db:migrate|seed: bun run db:seed" {
		t.Fatalf("%d %s %v", code, e, *calls)
	}
	if !strings.Contains(states, "seed=succeeded:") || !strings.Contains(states, "migrate=skipped:ran with the seed") {
		t.Fatal(states)
	}
}

func TestHostMigrateFailureStopsTheSeed(t *testing.T) {
	calls := stubHostExecCodes(t, map[string]int{"migrate": 3})
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, _, states, _, state := startHostSeed(t, hostSeedPlan(
		hostStep("migrate", knowledge.OnceSchema, "bun run db:migrate", ""),
		hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
	if code == 0 || strings.Join(*calls, "|") != "migrate: bun run db:migrate" || seededMarker(t, state) != "false" || !strings.Contains(states, "seed=failed:") {
		t.Fatalf("%d %v %s", code, *calls, states)
	}
}

func TestHostMigrateInAnotherDirRunsAtItsOwnPlace(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, e, states, _, _ := startHostSeed(t, hostSeedPlan(
		hostStep("migrate", knowledge.OnceSchema, "bun run db:migrate", "apps/api"),
		hostStep("seed", knowledge.OnceData, "bun run db:seed", "apps/web")))
	if code != 0 || strings.Join(*calls, "|") != "seed: bun run db:seed|migrate: bun run db:migrate" {
		t.Fatalf("%d %s %v", code, e, *calls)
	}
	if strings.Contains(states, "ran with the seed") {
		t.Fatal(states)
	}
}

// A host seed is not prefixed by a container migrate: each runs at its place.
func TestHostSeedWithAContainerMigrate(t *testing.T) {
	calls := stubHostExecCodes(t, nil)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	code, e, states, dlog, _ := startHostSeed(t, hostSeedPlan(
		knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "m"},
		hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
	if code != 0 || strings.Join(*calls, "|") != "seed: bun run db:seed" || strings.Count(dlog, " exec ") != 1 {
		t.Fatalf("%d %s %v\n%s", code, e, *calls, dlog)
	}
	if !strings.Contains(states, "migrate=succeeded:") {
		t.Fatal(states)
	}
}
