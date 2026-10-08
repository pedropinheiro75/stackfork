package cli

import (
	"bytes"
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

func TestLearnPrefillsPendingSeedAndVerifyNeedsADecision(t *testing.T) {
	root := seedScriptRoot(t)
	if _, stderr, code := runCLI(t, "--root", root, "learn"); code != 0 {
		t.Fatalf("learn: %s", stderr)
	}
	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil || k.Recipe.Migrate != "npm run db:migrate" || k.Recipe.SeedOnce != "npm run db:seed" || !k.Recipe.SeedPending {
		t.Fatalf("recipe %+v err %v", k.Recipe, err)
	}
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "p", "--up", "u")
	_, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "yes")
	if code == 0 || !strings.Contains(stderr, "seed script") || !strings.Contains(stderr, "--seed-once none") {
		t.Fatalf("verify must refuse while undecided: %d %s", code, stderr)
	}
	// re-running learn keeps the proposal pending
	runCLI(t, "--root", root, "learn")
	if k, _ := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json")); !k.Recipe.SeedPending {
		t.Fatal("rescan lost the pending proposal")
	}
	// an explicit "none" is a decision
	runCLI(t, "--root", root, "learn", "record", "recipe", "--seed-once", "none")
	if _, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "yes"); code != 0 {
		t.Fatalf("verify after none: %s", stderr)
	}
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

func TestStartEnvAsksSeedQuestionOnce(t *testing.T) {
	installFakeDocker(t)
	for _, c := range []struct{ answer, want string }{{"s\n", "npm run db:seed"}, {"never\n", "none"}} {
		root := seedScriptRoot(t)
		_, dir := seedPrepared(t, root)
		var out, errOut bytes.Buffer
		decideSeed(&out, &errOut, StartOptions{Interactive: true, In: strings.NewReader(c.answer)}, root, "p", dir)
		m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
		if m.SeedOnce != c.want || !strings.Contains(out.String(), "Seed the new database?") {
			t.Errorf("%q: manifest %q out %q", c.answer, m.SeedOnce, out.String())
		}
		out.Reset()
		decideSeed(&out, &errOut, StartOptions{Interactive: true, In: strings.NewReader("n\n")}, root, "p", dir)
		if out.Len() != 0 {
			t.Errorf("asked again after a decision: %q", out.String())
		}
	}
}

func TestStartEnvNonInteractiveWarnsAndRecordsNothing(t *testing.T) {
	installFakeDocker(t)
	root := seedScriptRoot(t)
	_, dir := seedPrepared(t, root)
	var out, errOut bytes.Buffer
	decideSeed(&out, &errOut, StartOptions{}, root, "sf-x", dir)
	if m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json")); m.SeedOnce != "" {
		t.Fatalf("recorded %q without an answer", m.SeedOnce)
	}
	if w := errOut.String(); !strings.Contains(w, "npm run db:seed") || !strings.Contains(w, "stackfork --instance sf-x seed") {
		t.Fatalf("warning %q", w)
	}
	// no seed script: silent
	errOut.Reset()
	decideSeed(&out, &errOut, StartOptions{}, initRepo(t), "p", dir)
	if errOut.Len() != 0 {
		t.Fatalf("unexpected output %q", errOut.String())
	}
}

func TestSeedCommandOnUndecidedRunsDetectedAndRecordsIt(t *testing.T) {
	log := installFakeDocker(t)
	root := seedScriptRoot(t)
	stateDir, dir := seedPrepared(t, root)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "seed"); code != 0 {
		t.Fatalf("seed: %s", stderr)
	}
	b, _ := os.ReadFile(log)
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if !strings.Contains(string(b), "npm run db:seed") || m.SeedOnce != "npm run db:seed" || m.Seeded == nil || !*m.Seeded {
		t.Fatalf("manifest %+v log %s", m, b)
	}
}
