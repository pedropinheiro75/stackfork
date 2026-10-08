package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
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

func seedScriptRoot(t *testing.T) string {
	t.Helper()
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"db:migrate":"m","db:seed":"s"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLearnPrefillsPendingSeedAndVerifyOnlyWarns(t *testing.T) {
	root := seedScriptRoot(t)
	if _, stderr, code := runCLI(t, "--root", root, "learn"); code != 0 {
		t.Fatalf("learn: %s", stderr)
	}
	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil || k.Recipe.Migrate != "npm run db:migrate" || k.Recipe.SeedOnce != "npm run db:seed" || !k.Recipe.SeedPending {
		t.Fatalf("recipe %+v err %v", k.Recipe, err)
	}
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "p", "--up", "u")
	out, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "yes")
	if code != 0 || !strings.Contains(out, "seeded with it automatically") {
		t.Fatalf("verify must pass with a warning: %d %s %s", code, out, stderr)
	}
	runCLI(t, "--root", root, "learn", "record", "recipe", "--seed-once", "none")
	if k, _ := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json")); k.Recipe.SeedOnce != "none" || k.Recipe.SeedPending {
		t.Fatalf("recipe %+v", k.Recipe)
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
	if code != 0 || !strings.Contains(out, "seeded new database with npm run db:seed") {
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
