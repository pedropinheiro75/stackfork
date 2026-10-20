package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Task 6.1: --secret NAME=env:VAR is stored by reference, resolved at up,
// and never appears in the manifest, the snapshot or a docker invocation.
func TestSecretEnvReferenceResolvedAtUp(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	const canary = "canary-env-secret-value-6-1"
	t.Setenv("SF_TEST_SECRET_TOKEN", canary)
	os.Unsetenv("npm_token")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "npm_token=env:SF_TEST_SECRET_TOKEN", "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, out)
	}
	stateDirOut := prepared["state"].(string)

	manifestData, err := os.ReadFile(filepath.Join(stateDirOut, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestData), `"npm_token": "env:SF_TEST_SECRET_TOKEN"`) {
		t.Fatalf("expected manifest to store the reference by name, got %s", manifestData)
	}
	if strings.Contains(string(manifestData), canary) {
		t.Fatal("manifest leaked the secret value")
	}

	configData, err := os.ReadFile(filepath.Join(stateDirOut, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"npm_token"`) {
		t.Fatalf("expected snapshot to declare the npm_token secret, got %s", configData)
	}
	if strings.Contains(string(configData), canary) {
		t.Fatal("snapshot leaked the secret value")
	}

	os.Truncate(log, 0)
	os.Remove(log + ".env")
	_, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	logData, _ := os.ReadFile(log)
	if strings.Contains(string(logData), canary) {
		t.Fatal("docker invocation log leaked the secret value")
	}
	// design D9: the value reaches the docker compose child only, under the
	// name and its alias, and never stackfork's own environment.
	env := dockerEnvLog(log)
	for _, kv := range []string{"npm_token=" + canary, "NPM_TOKEN=" + canary} {
		if !strings.Contains(env, kv[:strings.Index(kv, "=")+1]) || !strings.Contains(env, "\n"+kv+"\n") {
			t.Fatalf("the compose child did not receive %s", kv[:strings.Index(kv, "=")])
		}
	}
	for _, name := range []string{"npm_token", "NPM_TOKEN"} {
		if v := os.Getenv(name); v != "" {
			t.Fatalf("%s leaked into stackfork's own environment", name)
		}
	}
}

// Task 6.1: an unresolvable reference fails at up, naming the secret and
// the reference but never a partial value.
func TestSecretMissingVarFailsAtUp(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	os.Unsetenv("SF_TEST_MISSING_VAR")

	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "npm_token=env:SF_TEST_MISSING_VAR", "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, stderr)
	}
	if !strings.Contains(stderr, "npm_token") || !strings.Contains(stderr, "SF_TEST_MISSING_VAR") {
		t.Fatalf("expected error naming the secret and reference, got %s", stderr)
	}
}

// Task 6.1: a malformed --secret flag is refused at prepare, before
// anything is written.
func TestSecretMalformedFlagRefusedAtPrepare(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "not-a-valid-flag", "prepare", "-f", composePath)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	// design D1: the refusal never echoes input that did not parse.
	if strings.Contains(stderr, "not-a-valid-flag") || !strings.Contains(stderr, "--secret: expected NAME=REF") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

// Task 6.2: --npmrc PATH is an alias for --secret npm_token=npmrc:PATH#npm.pkg.github.com.
func TestNpmrcFlagAliasesSecret(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	registry := filepath.Join(t.TempDir(), "fixture.npmrc")
	if err := os.WriteFile(registry, []byte("//npm.pkg.github.com/:_authToken=synthetic-alias-check\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--npmrc", registry, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	var prepared map[string]interface{}
	json.Unmarshal([]byte(out), &prepared)
	manifestData, err := os.ReadFile(filepath.Join(prepared["state"].(string), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m state.Manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		t.Fatal(err)
	}
	resolvedRegistry, err := identity.ResolvePath(registry)
	if err != nil {
		t.Fatal(err)
	}
	want := "npmrc:" + resolvedRegistry + "#npm.pkg.github.com"
	if m.Secrets["npm_token"] != want {
		t.Fatalf("expected --npmrc to record %q, got %q", want, m.Secrets["npm_token"])
	}
	if strings.Contains(string(manifestData), "synthetic-alias-check") {
		t.Fatal("manifest leaked the npmrc token value")
	}
}

// Task 6.2: a manifest written before named secrets existed (only the
// legacy npmrc key) is read the same way as --secret npm_token=npmrc:PATH#npm.pkg.github.com.
func TestLegacyManifestNpmrcKeyReadAsNamedSecret(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "legacy.npmrc")
	npmrcPath := registry
	m := &state.Manifest{
		Project: "sf-x-000", Root: "/tmp/x", Branch: "main",
		Npmrc: &npmrcPath, Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	resolved := m.ResolvedSecrets()
	want := "npmrc:" + npmrcPath + "#npm.pkg.github.com"
	if resolved["npm_token"] != want {
		t.Fatalf("expected legacy npmrc key folded into npm_token, got %q", resolved["npm_token"])
	}

	// An explicit Secrets["npm_token"] wins over the legacy key.
	m.Secrets = map[string]string{"npm_token": "env:OTHER_VAR"}
	resolved = m.ResolvedSecrets()
	if resolved["npm_token"] != "env:OTHER_VAR" {
		t.Fatalf("expected the explicit secret to win, got %q", resolved["npm_token"])
	}
}

// Task 6.4 (canary): status, urls, where, the manifest and the snapshot
// never contain a secret value.
func TestSecretValueNeverLeaksIntoOutputsOrFiles(t *testing.T) {
	log := installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")

	const canary = "canary-leak-check-6-4"
	t.Setenv("SF_TEST_CANARY_VAR", canary)

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "npm_token=env:SF_TEST_CANARY_VAR", "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	if strings.Contains(out, canary) {
		t.Fatal("prepare output leaked the secret value")
	}
	var prepared map[string]interface{}
	json.Unmarshal([]byte(out), &prepared)
	stateDirOut := prepared["state"].(string)

	os.Truncate(log, 0)
	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	if strings.Contains(out, canary) || strings.Contains(stderr, canary) {
		t.Fatal("up output leaked the secret value")
	}

	t.Setenv("DOCKER_FAKE_STATUS", `{"ID":"c1","Name":"x-db-1","Service":"db","State":"running","Health":"","Publishers":[{"Protocol":"tcp","PublishedPort":54321,"TargetPort":5432,"URL":"127.0.0.1"}]}`+"\n")
	for _, action := range []string{"status", "urls", "where"} {
		out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, action)
		if code != 0 {
			t.Fatalf("%s failed: %s", action, stderr)
		}
		if strings.Contains(out, canary) {
			t.Fatalf("%s output leaked the secret value: %s", action, out)
		}
	}

	for _, name := range []string{"manifest.json", "compose.json"} {
		data, err := os.ReadFile(filepath.Join(stateDirOut, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), canary) {
			t.Fatalf("%s leaked the secret value", name)
		}
	}

	logData, _ := os.ReadFile(log)
	if strings.Contains(string(logData), canary) {
		t.Fatal("docker invocation log leaked the secret value")
	}
}
