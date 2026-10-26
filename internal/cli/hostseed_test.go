package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	stpkg "github.com/pedropinheiro75/stackfork/internal/state"
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
	if code != 0 || strings.Join(*calls, "|") != "migrate: bun run db:migrate|seed: bun run db:seed" {
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

// up and seed run a host seed only on a trusted plan, and refuse before any
// Docker call, a run file or a host command, with start-env's message.
func TestUpAndSeedRefuseAnUntrustedHostSeed(t *testing.T) {
	for _, cmd := range []string{"up", "seed"} {
		t.Run(cmd, func(t *testing.T) {
			root, state := reposEnv(t)
			repo := planRepo(t, root, hostSeedPlan(hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
			log := installFakeDocker(t)
			calls := stubHostExecCodes(t, nil)
			base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
			if _, e, code := runCLI(t, append(base, "--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml")...); code != 0 {
				t.Fatal(e)
			}
			os.Remove(log) // only what the refused command does counts
			_, e, code := runCLI(t, append(base, "--root", repo, cmd)...)
			if code == 0 || !strings.Contains(e, "not trusted on this machine") || !strings.Contains(e, "stackfork plan --trust") || !strings.Contains(e, "on: host") {
				t.Fatalf("%d %s", code, e)
			}
			if b, _ := os.ReadFile(log); len(b) != 0 || len(*calls) != 0 || len(runFiles(state)) != 0 {
				t.Fatalf("docker, the host or a run file was touched: %q %v %v", b, *calls, runFiles(state))
			}
		})
	}
}

func TestSeedRunsAHostSeedOnATrustedPlan(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hostSeedPlan(hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
	calls := stubHostExecCodes(t, nil)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	if _, e, code := runCLI(t, append(base, "--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml")...); code != 0 {
		t.Fatal(e)
	}
	if out, e, code := runCLI(t, append(base, "--root", repo, "seed")...); code != 0 || strings.Join(*calls, "|") != "seed: bun run db:seed" {
		t.Fatalf("%d %s %s %v", code, out, e, *calls)
	}
}

// swapSeedWhileLocked trusts the plan, then runs `up` or `seed` while the
// instance lock is held; once the command has passed its trust check (its run
// file exists) it rewrites the seed command in knowledge.json and lets go.
func swapSeedWhileLocked(t *testing.T, cmd string) (calls *[]string, code int, stderr, dockerLog string) {
	t.Helper()
	root, state := reposEnv(t)
	repo := planRepo(t, root, hostSeedPlan(hostStep("seed", knowledge.OnceData, "bun run db:seed", "")))
	calls = stubHostExecCodes(t, nil)
	log := installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	if _, e, c := runCLI(t, append(base, "--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml")...); c != 0 {
		t.Fatal(e)
	}
	lock, err := stpkg.AcquireLock(filepath.Join(onlyRunDir(t, state), ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, stderr, code = runCLI(t, append(base, "--root", repo, cmd)...)
	}()
	for i := 0; len(runFiles(state)) == 0; i++ {
		if i > 500 {
			t.Fatal("the command never started its run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	path := filepath.Join(repo, ".stackfork", "knowledge.json")
	k, err := knowledge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range k.Plan.Steps {
		if k.Plan.Steps[i].ID == "seed" {
			k.Plan.Steps[i].Cmd = "evil-command"
		}
	}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	lock.Release()
	<-done
	b, _ := os.ReadFile(log)
	return calls, code, stderr, string(b)
}

// The plan the check trusted is the plan that runs: a seed command rewritten
// after the check, while the run waits for the lock, never reaches the host.
func TestUpAndSeedRefuseAHostSeedSwappedAfterTheTrustCheck(t *testing.T) {
	for _, cmd := range []string{"up", "seed"} {
		t.Run(cmd, func(t *testing.T) {
			calls, code, e, dlog := swapSeedWhileLocked(t, cmd)
			if len(*calls) != 0 {
				t.Fatalf("the host ran %v (exit %d): %s", *calls, code, e)
			}
			if !strings.Contains(e, "the plan changed since it was checked; review it and run `stackfork plan --trust`") {
				t.Fatalf("refusal text missing:\n%s", e)
			}
			if strings.Contains(dlog, "evil-command") { // refused, not run in a container either
				t.Fatalf("the swapped command reached Docker:\n%s", dlog)
			}
		})
	}
}
