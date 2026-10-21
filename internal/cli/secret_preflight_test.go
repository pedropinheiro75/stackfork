package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// preflightFixture is a ready repo, a fake docker, an empty fake keychain and
// the args every command takes. The returned repo is the resolved main path.
func preflightFixture(t *testing.T, declared ...knowledge.SecretDecl) (repo string, base []string, dockerLog, kc string) {
	t.Helper()
	root, state := reposEnv(t)
	repo = adaptedRepo(t, root)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Secrets: declared}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	dockerLog = installFakeDocker(t)
	kc = fakeKeychain(t, nil)
	return repo, []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}, dockerLog, kc
}

// runsInstance is the only instance directory with runs; a Start that failed
// before prepare has no manifest.
func runsInstance(t *testing.T, stateDir string) string {
	t.Helper()
	ds, _ := filepath.Glob(filepath.Join(stateDir, "*", "runs"))
	if len(ds) != 1 {
		t.Fatalf("instances with runs: %v", ds)
	}
	return filepath.Dir(ds[0])
}

func secretsStepLog(t *testing.T, base []string, repo string) (log string, r *runs.Run) {
	t.Helper()
	dir := runsInstance(t, base[1])
	r, err := runs.Get(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := runs.Log(dir, 1, "secrets")
	if err != nil {
		t.Fatalf("secrets step log: %v", err)
	}
	return strings.ReplaceAll(string(b), repo, "REPO"), r
}

func noDockerCalls(t *testing.T, dockerLog string) {
	t.Helper()
	// the read-only inventory (inspect, ps) runs before the run begins; no
	// Compose command may have
	b, _ := os.ReadFile(dockerLog)
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" && !strings.HasPrefix(line, "inspect ") && !strings.HasPrefix(line, "ps ") {
			t.Fatalf("docker was called after a failed preflight:\n%s", b)
		}
	}
}

func TestPreflightMissingKeychainItemFailsBeforeDocker(t *testing.T) {
	repo, base, dockerLog, _ := preflightFixture(t)
	mapSecret(t, repo, "NPM_TOKEN", "keychain:acme-npm-token")
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "step secrets failed") || !strings.Contains(e, "NPM_TOKEN (keychain:acme-npm-token) missing") {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	noDockerCalls(t, dockerLog)
	log, r := secretsStepLog(t, base, repo)
	assertGolden(t, "preflight-missing-keychain.log", log)
	if r.Outcome != runs.Failed || stepStates(r) != "sync=succeeded: secrets=failed:" {
		t.Fatalf("%s %s", r.Outcome, stepStates(r))
	}
}

func TestPreflightLockedKeychain(t *testing.T) {
	repo, base, dockerLog, kc := preflightFixture(t)
	os.WriteFile(filepath.Join(kc, "locked"), nil, 0o600)
	mapSecret(t, repo, "NPM_TOKEN", "keychain:acme-npm-token")
	if _, _, code := runCLI(t, append(base, "start-env", repo)...); code == 0 {
		t.Fatal("expected a failure")
	}
	noDockerCalls(t, dockerLog)
	log, _ := secretsStepLog(t, base, repo)
	assertGolden(t, "preflight-locked.log", log)
}

func TestPreflightEnvReferenceFromTheShellAndFromTheDashboard(t *testing.T) {
	repo, base, dockerLog, _ := preflightFixture(t)
	os.Unsetenv("SF_TEST_PREFLIGHT_VAR")
	mapSecret(t, repo, "NPM_TOKEN", "env:SF_TEST_PREFLIGHT_VAR")
	if _, _, code := runCLI(t, append(base, "start-env", repo)...); code == 0 {
		t.Fatal("expected a failure")
	}
	log, _ := secretsStepLog(t, base, repo)
	assertGolden(t, "preflight-env-cli.log", log)

	// the same Start from the dashboard says why the shell variable is unseen
	var out bytes.Buffer
	err := StartEnv(&out, &out, StartOptions{Checkout: repo, NewStateBase: base[1], LegacyStateBase: base[3], Trigger: "dashboard"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	b, _ := runs.Log(runsInstance(t, base[1]), 2, "secrets")
	assertGolden(t, "preflight-env-dashboard.log", strings.ReplaceAll(string(b), repo, "REPO"))
	noDockerCalls(t, dockerLog)
}

func TestPreflightDeclaredNameWithoutReferenceListsEveryProblem(t *testing.T) {
	repo, base, dockerLog, _ := preflightFixture(t,
		knowledge.SecretDecl{Name: "NPM_TOKEN", Hint: "GitHub Packages read token"},
		knowledge.SecretDecl{Name: "STRIPE_TEST_KEY"})
	mapSecret(t, repo, "STRIPE_TEST_KEY", "keychain:acme-stripe")
	if _, e, code := runCLI(t, append(base, "start-env", repo)...); code == 0 || !strings.Contains(e, "NPM_TOKEN has no reference") || !strings.Contains(e, "STRIPE_TEST_KEY (keychain:acme-stripe) missing") {
		t.Fatalf("%d %s", code, e)
	}
	noDockerCalls(t, dockerLog)
	log, _ := secretsStepLog(t, base, repo)
	assertGolden(t, "preflight-declared-and-missing.log", log)
}

func TestPreflightResolvesAndKeepsValuesOutOfTheLog(t *testing.T) {
	repo, base, _, _ := preflightFixture(t)
	const canary = "preflight-canary-value-4-1"
	fakeKeychain(t, map[string]string{"acme-npm-token": canary})
	mapSecret(t, repo, "NPM_TOKEN", "keychain:acme-npm-token")
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 || strings.Contains(out+e, canary) {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	log, r := secretsStepLog(t, base, repo)
	assertGolden(t, "preflight-resolves.log", log)
	if stepStates(r) != "sync=succeeded: secrets=succeeded: prepare=succeeded: up=succeeded:" {
		t.Fatal(stepStates(r))
	}
	dir := runsInstance(t, base[1])
	filepath.WalkDir(dir, func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && filepath.Base(p) != "compose.json" {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), canary) {
				t.Errorf("%s holds the value", p)
			}
		}
		return nil
	})
}

func TestPreflightBareUpAndSeedFailBeforeDocker(t *testing.T) {
	root, stateDir, _ := secretRepo(t, "TOK=env:SF_TEST_PREFLIGHT_TOK")
	mapSecret(t, root, "TOK", "keychain:acme-missing") // a store reference comes from the map, not a flag
	dockerLog := installFakeDocker(t)
	fakeKeychain(t, nil)
	_, e, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	if code != 1 || !strings.Contains(e, "TOK (keychain:acme-missing) missing") {
		t.Fatalf("%d %s", code, e)
	}
	noDockerCalls(t, dockerLog)
}
