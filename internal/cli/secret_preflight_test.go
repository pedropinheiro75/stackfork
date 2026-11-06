package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
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
	// the read-only inventory (inspect, ps) runs before the run begins, and
	// the preflight reads the Compose source with `compose config`; no other
	// Compose command may have
	b, _ := os.ReadFile(dockerLog)
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" && !strings.HasPrefix(line, "inspect ") && !strings.HasPrefix(line, "ps ") && !strings.Contains(line, " config --no-env-resolution ") {
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

// A name the Compose file uses needs a reference on a bare up and seed too,
// not only on a Start: it fails at the secrets step, not later in Docker.
func TestPreflightBareUpAndSeedCheckTheNamesComposeUses(t *testing.T) {
	repo, base, dockerLog := usedSecretFixture(t)
	prepareWith(t, repo, base[1], filepath.Join(repo, ".agent-dev", "app.compose.yaml"))
	os.Truncate(dockerLog, 0)
	_, e, code := runCLI(t, "--root", repo, "--state-dir", base[1], "--legacy-state-dir", base[3], "up")
	if code == 0 || !strings.Contains(e, "step secrets failed: npm_token has no reference") {
		t.Fatalf("%d %s", code, e)
	}
	noDockerCalls(t, dockerLog)
}

// A reference given once by --secret stays in the manifest: the failure says
// how to get out, and the other references get no such hint.
func TestPreflightRecordedReferenceSaysHowToReplaceIt(t *testing.T) {
	root, stateDir, _ := secretRepo(t, "TOK=npmrc:/nonexistent/sf.npmrc#registry.example.com")
	dockerLog := installFakeDocker(t)
	_, e, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	if code == 0 || !strings.Contains(e, "step secrets failed") {
		t.Fatalf("%d %s", code, e)
	}
	log, _ := secretsStepLog(t, []string{"", stateDir}, root)
	if !strings.Contains(log, "or give prepare a new --secret TOK=REF, or run: stackfork secret forget TOK") {
		t.Fatalf("%s", log)
	}
	noDockerCalls(t, dockerLog)
}

// `start SERVICE` runs the same secrets step as `up`: a name the Compose file
// uses and nothing supplies fails with the fix before any Compose call, not
// with Compose's own message.
func TestPreflightStartServiceChecksTheNamesComposeUses(t *testing.T) {
	repo, base, dockerLog := usedSecretFixture(t)
	prepareWith(t, repo, base[1], filepath.Join(repo, ".agent-dev", "app.compose.yaml"))
	os.Truncate(dockerLog, 0)
	_, e, code := runCLI(t, "--root", repo, "--state-dir", base[1], "--legacy-state-dir", base[3], "start", "app")
	if code == 0 || !strings.Contains(e, "npm_token is used by app.compose.yaml but has no reference; run: stackfork secret map") {
		t.Fatalf("%d %s", code, e)
	}
	noDockerCalls(t, dockerLog)
	if ds, _ := filepath.Glob(filepath.Join(base[1], "*", "runs")); len(ds) != 0 {
		t.Fatalf("a Start of one service recorded a run: %v", ds)
	}
}

// Inside a worktree, `secret ls` and `secret check` without REPO describe this
// checkout's recorded reference, the one a plain up there uses. Another
// checkout's reference is the sibling row; with REPO nothing changes.
func TestSecretLsInAWorktreeShowsThisCheckoutsReference(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	os.Unsetenv("SF_TEST_MAIN_TOKEN")
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", repo, "--secret", "tok=env:SF_TEST_MAIN_TOKEN",
		"prepare", "--force", "-f", "docker-compose.yml")...); code != 0 {
		t.Fatal(e)
	}
	row := func(args ...string) (secretRowFixture, int) {
		out, e, code := runCLI(t, append(append(append([]string{}, base...), args...), "--json")...)
		var rows []secretRowFixture
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("%v: %s %s", err, out, e)
		}
		for _, r := range rows {
			if r.Name == "tok" {
				return r, code
			}
		}
		t.Fatalf("no tok row: %s", out)
		return secretRowFixture{}, 0
	}
	if r, code := row("--root", sib, "secret", "check"); r.Reference != "env:"+sibTokenName || r.Source != "manifest" || code != 0 {
		t.Fatalf("in the worktree: %+v exit %d", r, code)
	}
	if r, code := row("--root", repo, "secret", "check"); r.Reference != "env:SF_TEST_MAIN_TOKEN" || r.Source != "manifest" || code != 1 {
		t.Fatalf("in the main checkout: %+v exit %d", r, code)
	}
	if r, _ := row("--root", sib, "secret", "ls", repo); r.Reference != "env:SF_TEST_MAIN_TOKEN" || r.Source != "manifest" {
		t.Fatalf("with REPO: %+v", r)
	}
}

type secretRowFixture struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	Source    string `json:"source"`
}

// usedSecretCompose uses npm_token (nothing supplies it) and tok_file (a file
// supplies it): only the first needs a reference.
const usedSecretCompose = "services:\n  app:\n    image: alpine\n    secrets: [npm_token, tok_file]\nsecrets:\n  npm_token:\n    environment: SF_TEST_USED_SECRET_UNSET\n  tok_file:\n    file: ./tok.txt\n"

func usedSecretFixture(t *testing.T) (repo string, base []string, dockerLog string) {
	t.Helper()
	testsupport.RequireCompose(t)
	os.Unsetenv("SF_TEST_USED_SECRET_UNSET")
	repo, base, dockerLog, _ = preflightFixture(t)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(usedSecretCompose), 0o644)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "tok.txt"), []byte("x"), 0o600)
	return repo, base, dockerLog
}

func TestPreflightComposeSecretWithoutReferenceFailsBeforePrepare(t *testing.T) {
	repo, base, dockerLog := usedSecretFixture(t)
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "step secrets failed: npm_token has no reference") {
		t.Fatalf("%d %s", code, e)
	}
	noDockerCalls(t, dockerLog)
	log, _ := secretsStepLog(t, base, repo)
	if !strings.Contains(log, "npm_token is used by app.compose.yaml but has no reference; run: stackfork secret map REPO npm_token=npmrc:") || strings.Contains(log, "tok_file") {
		t.Fatalf("%s", log)
	}
	if ms, _ := filepath.Glob(filepath.Join(base[1], "*", "manifest.json")); len(ms) != 0 {
		t.Fatalf("prepare ran: %v", ms)
	}
}

func TestComposeSecretCountsAsDeclaredForFolderMapsAndLs(t *testing.T) {
	repo, base, _ := usedSecretFixture(t)
	out, _, code := runCLI(t, append(base, "secret", "ls", repo)...)
	if code != 0 || !strings.Contains(out, "npm_token") || !strings.Contains(out, "missing") || strings.Contains(out, "tok_file") {
		t.Fatalf("%d %s", code, out)
	}
	mapSecret(t, filepath.Dir(repo), "npm_token", "keychain:acme-npm-token")
	out, _, _ = runCLI(t, append(base, "secret", "ls", repo)...)
	if !strings.Contains(out, "folder") || !strings.Contains(out, "keychain:acme-npm-token") {
		t.Fatalf("a folder map must apply to a name the Compose file uses:\n%s", out)
	}
	fakeKeychain(t, map[string]string{"acme-npm-token": "v"})
	if _, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s", code, e)
	}
}
