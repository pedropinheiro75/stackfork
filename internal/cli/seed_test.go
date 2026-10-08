package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
