package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const seedCompose = "services:\n  db:\n    image: postgres:17\n    volumes:\n      - pgdata:/var/lib/postgresql/data\nvolumes:\n  pgdata: {}\n"

func seedFixture(t *testing.T) (root, stateDir, log string) {
	t.Helper()
	log = installFakeDocker(t)
	root = initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(seedCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	k := `{"schema":1,"recipe":{"seed_once":"bun run db:seed","seed_service":"db"}}`
	if err := os.MkdirAll(filepath.Join(root, ".stackfork"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".stackfork", "knowledge.json"), []byte(k), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir = filepath.Join(t.TempDir(), "state")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	return
}

func seedExecs(t *testing.T, log string) int {
	b, _ := os.ReadFile(log)
	return strings.Count(string(b), "bun run db:seed")
}

func seededMarker(t *testing.T, stateDir string) string {
	t.Helper()
	ms, _ := filepath.Glob(filepath.Join(stateDir, "*", "manifest.json"))
	m, err := state.ReadManifest(ms[0])
	if err != nil || m == nil {
		t.Fatal(err)
	}
	switch {
	case m.Seeded == nil:
		return "nil"
	case *m.Seeded:
		return "true"
	}
	return "false"
}

func TestSeedOnceAcrossUpDownUp(t *testing.T) {
	root, stateDir, log := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1") // the volume does not exist yet
	args := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(args, "up")...); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	if n := seedExecs(t, log); n != 1 || seededMarker(t, stateDir) != "true" {
		t.Fatalf("first up: %d seeds, marker %s", n, seededMarker(t, stateDir))
	}
	for _, a := range []string{"down", "up", "up"} {
		runCLI(t, append(args, a)...)
	}
	runCLI(t, append(args, "restart", "db")...)
	if n := seedExecs(t, log); n != 1 {
		t.Fatalf("seeded %d times across down/up/restart", n)
	}
}

func TestSeedNotRunWhenDataAlreadyThere(t *testing.T) {
	root, stateDir, log := seedFixture(t) // volume "exists" (fake default)
	runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if n := seedExecs(t, log); n != 0 || seededMarker(t, stateDir) != "true" {
		t.Fatalf("%d seeds, marker %s", n, seededMarker(t, stateDir))
	}
}

// The up that creates the volume fails (e.g. app not healthy in time): the
// volume exists on the next up, which must still seed it.
func TestFailedUpLeavesSeedPendingForNextUp(t *testing.T) {
	root, stateDir, log := seedFixture(t)
	args := []string{"--root", root, "--state-dir", stateDir}
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXIT", "1")
	runCLI(t, append(args, "up")...)
	if n := seedExecs(t, log); n != 0 || seededMarker(t, stateDir) != "false" {
		t.Fatalf("failed up: %d seeds, marker %s", n, seededMarker(t, stateDir))
	}
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "0") // the failed up created it
	t.Setenv("DOCKER_FAKE_EXIT", "0")
	if _, stderr, code := runCLI(t, append(args, "up")...); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	runCLI(t, append(args, "up")...)
	if n := seedExecs(t, log); n != 1 || seededMarker(t, stateDir) != "true" {
		t.Fatalf("next ups: %d seeds, marker %s", n, seededMarker(t, stateDir))
	}
}

func TestSeedFailureLeavesFalseAndSeedCommandRetries(t *testing.T) {
	root, stateDir, log := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	args := []string{"--root", root, "--state-dir", stateDir}
	_, stderr, _ := runCLI(t, append(args, "up")...)
	if seededMarker(t, stateDir) != "false" || !strings.Contains(stderr, "retry with `stackfork --instance ") || !strings.Contains(stderr, " seed`") {
		t.Fatalf("marker %s stderr %s", seededMarker(t, stateDir), stderr)
	}
	// a later up does not auto-retry
	runCLI(t, append(args, "up")...)
	if n := seedExecs(t, log); n != 1 {
		t.Fatalf("auto-retried: %d", n)
	}
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "0")
	if _, stderr, code := runCLI(t, append(args, "seed")...); code != 0 {
		t.Fatalf("seed: %s", stderr)
	}
	if seededMarker(t, stateDir) != "true" || seedExecs(t, log) != 2 {
		t.Fatalf("after retry: %s, %d", seededMarker(t, stateDir), seedExecs(t, log))
	}
	out, _, _ := runCLI(t, append(args, "seed")...)
	if !strings.Contains(out, "already seeded") || seedExecs(t, log) != 2 {
		t.Fatalf("second seed ran: %s", out)
	}
	_, stderr, _ = runCLI(t, append(args, "seed", "--force")...)
	if seedExecs(t, log) != 3 || !strings.Contains(stderr, "warning") {
		t.Fatalf("force: %d %s", seedExecs(t, log), stderr)
	}
}

func TestSeedMarkDoneRecordsWithoutRunning(t *testing.T) {
	root, stateDir, log := seedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	args := []string{"--root", root, "--state-dir", stateDir}
	runCLI(t, append(args, "up")...)
	if logs, _ := filepath.Glob(filepath.Join(stateDir, "*", "seed.log")); len(logs) != 0 { // the step log replaces it
		t.Fatalf("seed.log written: %v", logs)
	}
	if out, stderr, code := runCLI(t, append(args, "seed", "--mark-done")...); code != 0 || !strings.Contains(out, "nothing ran") {
		t.Fatalf("mark-done: %s %s", out, stderr)
	}
	if seededMarker(t, stateDir) != "true" || seedExecs(t, log) != 1 {
		t.Fatalf("after mark-done: %s, %d", seededMarker(t, stateDir), seedExecs(t, log))
	}
}

func seedScriptRoot(t *testing.T) string {
	t.Helper()
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"db:migrate":"m","db:seed":"s"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLearnProposesSeedAndRecordNoneRemovesIt(t *testing.T) {
	root := seedScriptRoot(t)
	if _, stderr, code := runCLI(t, "--root", root, "learn"); code != 0 {
		t.Fatalf("learn: %s", stderr)
	}
	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil || k.Plan.MigrateStep() == nil || k.Plan.MigrateStep().Cmd != "npm run db:migrate" || k.Plan.SeedStep() == nil || k.Plan.SeedStep().Cmd != "npm run db:seed" || k.Plan.Status != knowledge.PlanProposed {
		t.Fatalf("plan %+v err %v", k.Plan, err)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "yes"); code == 0 || !strings.Contains(stderr, "learn record step") {
		t.Fatalf("a proposed plan must not verify: %d %s", code, stderr)
	}
	runCLI(t, "--root", root, "learn", "record", "recipe", "--seed-once", "none")
	if k, _ := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json")); k.Plan.SeedStep() != nil || k.Plan.Status != knowledge.PlanRecorded {
		t.Fatalf("plan %+v", k.Plan)
	}
	if decided, c, _ := learn.SeedDecision([]string{root}, nil); !decided || learn.SeedActive(c) {
		t.Fatalf("none must read back as never: %v %q", decided, c)
	}
}

func seedPrepared(t *testing.T, root string) (stateDir, instanceDir string) {
	t.Helper()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(seedCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir = filepath.Join(t.TempDir(), "state")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	ms, _ := filepath.Glob(filepath.Join(stateDir, "*", "manifest.json"))
	return stateDir, filepath.Dir(ms[0])
}

// autoSeedFixture: a prepared instance whose checkout has a seed script and
// no recorded decision.
func autoSeedFixture(t *testing.T) (root, stateDir, dir, log string) {
	log = installFakeDocker(t)
	root = seedScriptRoot(t)
	stateDir, dir = seedPrepared(t, root)
	return
}

func TestNewVolumeIsSeededAutomaticallyOnceWithNoQuestion(t *testing.T) {
	root, stateDir, _, log := autoSeedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	args := []string{"--root", root, "--state-dir", stateDir}
	out, stderr, code := runCLI(t, append(args, "up")...)
	if code != 0 || !strings.Contains(out, "seeded new database with npm run db:migrate && npm run db:seed") {
		t.Fatalf("up: %d %s %s", code, out, stderr)
	}
	for _, a := range []string{"down", "up", "restart"} {
		sub := []string{a}
		if a == "restart" {
			sub = append(sub, "db")
		}
		runCLI(t, append(args, sub...)...)
	}
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "npm run db:seed"); n != 1 {
		t.Fatalf("seeded %d times", n)
	}
	// start-env never asks anything either
	if strings.Contains(out, "?") {
		t.Fatalf("a question was printed: %s", out)
	}
}

func TestExistingInstanceIsNeverSeededOrAsked(t *testing.T) {
	root, stateDir, dir, log := autoSeedFixture(t) // fake docker: the volume already exists
	out, _, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	b, _ := os.ReadFile(log)
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if code != 0 || strings.Contains(string(b), "npm run db:seed") || strings.Contains(out, "seed") || m.Seeded == nil || !*m.Seeded {
		t.Fatalf("code %d out %q seeded %v log %s", code, out, m.Seeded, b)
	}
}

func TestNoneDisablesTheAutomaticSeed(t *testing.T) {
	root, stateDir, dir, log := autoSeedFixture(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")

	if err := os.MkdirAll(filepath.Join(root, ".stackfork"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".stackfork", "knowledge.json"), []byte(`{"schema":1,"recipe":{"seed_once":"none"}}`), 0o644)
	runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	b, _ := os.ReadFile(log)
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if strings.Contains(string(b), "npm run db:seed") || m.Seeded != nil {
		t.Fatalf("none was ignored: %v %s", m.Seeded, b)
	}
}

func TestStatelessStackIsNeverSeeded(t *testing.T) {
	log := installFakeDocker(t)
	root := seedScriptRoot(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte("services:\n  db:\n    image: postgres:17\n"), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "db:seed") {
		t.Fatalf("seeded a stack with no volume: %s", b)
	}
}

func TestSeedCommandOnUndecidedRunsTheDetectedScript(t *testing.T) {
	log := installFakeDocker(t)
	root := seedScriptRoot(t)
	stateDir, dir := seedPrepared(t, root)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "seed"); code != 0 {
		t.Fatalf("seed: %s", stderr)
	}
	b, _ := os.ReadFile(log)
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if !strings.Contains(string(b), "npm run db:seed") || m.Seeded == nil || !*m.Seeded {
		t.Fatalf("manifest %+v log %s", m, b)
	}
}

// The dashboard's retry hint is `stackfork --instance NAME seed`.
func TestSeedAcceptsInstanceFlag(t *testing.T) {
	root, stateDir, _, _ := autoSeedFixture(t)
	out, _, _ := runCLI(t, "--root", root, "--state-dir", stateDir, "where")
	var w struct{ Project string }
	json.Unmarshal([]byte(out), &w)
	_, stderr, _ := runCLI(t, "--state-dir", stateDir, "--instance", w.Project, "seed")
	if strings.Contains(stderr, "not supported") {
		t.Fatal(stderr)
	}
}

func TestUpMigratesThenSeedsFromPlan(t *testing.T) {
	root, stateDir, log := seedFixture(t)
	k := `{"schema":2,"plan":{"status":"recorded","steps":[{"id":"up","kind":"compose-up"},{"id":"migrate","kind":"exec","cmd":"m","once":"schema"},{"id":"seed","kind":"exec","service":"db","cmd":"bun run db:seed","once":"data"}]}}`
	if err := os.WriteFile(filepath.Join(root, ".stackfork", "knowledge.json"), []byte(k), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "m && bun run db:seed") {
		t.Fatalf("expected the migrate then seed exec, log:\n%s", b)
	}
}

func writeKnowledgeJSON(t *testing.T, root, doc string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".stackfork"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".stackfork", "knowledge.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSchema1EmptySeedStaysAutoDetect(t *testing.T) {
	root := seedScriptRoot(t)
	writeKnowledgeJSON(t, root, `{"schema":1,"recipe":{"prepare":"stackfork prepare -f a.yaml","up":"stackfork up"}}`)
	if d, _, _ := learn.SeedDecision([]string{root}, nil); d {
		t.Fatal("empty seed_once must stay undecided")
	}
	cmd, _ := lifecycle.SeedRecipe(root, root, t.TempDir())
	if cmd != "npm run db:migrate && npm run db:seed" {
		t.Fatalf("detected seed with migrate prefix expected, got %q", cmd)
	}
}

func TestSeedNoneIsExplicit(t *testing.T) {
	root := seedScriptRoot(t)
	writeKnowledgeJSON(t, root, `{"schema":1,"recipe":{"seed_once":"none"}}`)
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	k, err := knowledge.Load(path)
	if err != nil || !k.Plan.NoSeed || k.Plan.SeedStep() != nil {
		t.Fatalf("plan %+v err %v", k.Plan, err)
	}
	if cmd, _ := lifecycle.SeedRecipe(root, root, t.TempDir()); cmd != "" {
		t.Fatalf("none must not seed, got %q", cmd)
	}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	if r, err := knowledge.Load(path); err != nil || !r.Plan.NoSeed {
		t.Fatalf("no_seed lost in round trip: %+v %v", r, err)
	}
	if cmd, _ := lifecycle.SeedRecipe(root, root, t.TempDir()); cmd != "" {
		t.Fatalf("none must not seed after a round trip, got %q", cmd)
	}
}

// record recipe marks the plan recorded and keeps the scan's proposed seed
// step, so the detected seed will run: the same outcome as before. A seed
// step removed on its own stays undecided; --once none is the explicit no.
func TestRecordRecipeKeepsProposedSeedAndNoneIsExplicit(t *testing.T) {
	root := seedScriptRoot(t)
	runCLI(t, "--root", root, "learn")
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "p", "--up", "u")
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	k, _ := knowledge.Load(path)
	if k.Plan.Status != knowledge.PlanRecorded || k.Plan.SeedStep() == nil || k.Plan.SeedStep().Cmd != "npm run db:seed" {
		t.Fatalf("proposed seed must stay in the recorded plan: %+v", k.Plan)
	}
	id := k.Plan.SeedStep().ID
	runCLI(t, "--root", root, "learn", "record", "step", "--remove", id)
	k, _ = knowledge.Load(path)
	if k.Plan.NoSeed || k.Plan.SeedStep() != nil {
		t.Fatalf("--remove must not set no_seed: %+v", k.Plan)
	}
	runCLI(t, "--root", root, "learn", "record", "step", "--id", "seed", "--kind", "exec", "--cmd", "s", "--once", "data")
	runCLI(t, "--root", root, "learn", "record", "step", "--id", "seed", "--kind", "exec", "--once", "none")
	k, _ = knowledge.Load(path)
	if !k.Plan.NoSeed || k.Plan.SeedStep() != nil {
		t.Fatalf("--once none must set no_seed and drop the step: %+v", k.Plan)
	}
	runCLI(t, "--root", root, "learn", "record", "step", "--id", "seed", "--kind", "exec", "--cmd", "s", "--once", "data")
	if k, _ = knowledge.Load(path); k.Plan.NoSeed || k.Plan.SeedStep() == nil {
		t.Fatalf("recording a data step clears no_seed: %+v", k.Plan)
	}
}
