package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// secretRepo prepares a fresh repo with the given --secret flags and returns
// the root, state dir and instance dir. The config dir is private to the test.
func secretRepo(t *testing.T, secrets ...string) (root, stateDir, instanceDir string) {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	root = initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(fixtureCompose), 0o600)
	stateDir = filepath.Join(t.TempDir(), "state")
	instanceDir = prepareWith(t, root, stateDir, composePath, secrets...)
	return root, stateDir, instanceDir
}

func prepareWith(t *testing.T, root, stateDir, composePath string, secrets ...string) string {
	t.Helper()
	args := []string{"--root", root, "--state-dir", stateDir}
	for _, s := range secrets {
		args = append(args, "--secret", s)
	}
	out, stderr, code := runCLI(t, append(args, "prepare", "-f", composePath)...)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	var p map[string]interface{}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	return p["state"].(string)
}

func mapSecret(t *testing.T, repo, name, ref string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Map(repo, name, ref); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func manifestRefs(t *testing.T, instanceDir string) map[string]string {
	t.Helper()
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil || m == nil {
		t.Fatalf("manifest: %v", err)
	}
	return m.ResolvedSecrets()
}

// D7 precedence at prepare: flags beat the map, the map beats the manifest.
func TestPrepareSecretPrecedence(t *testing.T) {
	installFakeDocker(t)
	root, stateDir, inst := secretRepo(t, "A=env:SF_TEST_A1")
	composePath := filepath.Join(root, "compose.yaml")
	if got := manifestRefs(t, inst)["A"]; got != "env:SF_TEST_A1" {
		t.Fatalf("manifest level: %q", got)
	}

	mapSecret(t, root, "A", "env:SF_TEST_A2")
	mapSecret(t, root, "B", "env:SF_TEST_B")
	prepareWith(t, root, stateDir, composePath)
	refs := manifestRefs(t, inst)
	if refs["A"] != "env:SF_TEST_A2" || refs["B"] != "env:SF_TEST_B" {
		t.Fatalf("map must beat the manifest and add new names: %v", refs)
	}

	prepareWith(t, root, stateDir, composePath, "A=env:SF_TEST_A3")
	if got := manifestRefs(t, inst)["A"]; got != "env:SF_TEST_A3" {
		t.Fatalf("flag must beat the map: %q", got)
	}
}

func TestEffectiveLayersSiblingIsLowest(t *testing.T) {
	refs, src := lifecycle.Effective(
		lifecycle.Layer{Source: lifecycle.SourceSibling, Refs: map[string]string{"N": "env:SIB", "S": "env:SIB"}},
		lifecycle.Layer{Source: lifecycle.SourceManifest, Refs: map[string]string{"N": "env:MAN", "M": "env:MAN"}},
		lifecycle.Layer{Source: lifecycle.SourceMap, Refs: map[string]string{"N": "env:MAP", "P": "env:MAP"}},
		lifecycle.Layer{Source: lifecycle.SourceFlag, Refs: map[string]string{"N": "env:FLAG"}},
	)
	want := map[string]string{"N": "flag", "S": "sibling", "M": "manifest", "P": "map"}
	for n, s := range want {
		if src[n] != s {
			t.Fatalf("%s: source %s, want %s", n, src[n], s)
		}
	}
	if refs["N"] != "env:FLAG" || refs["S"] != "env:SIB" {
		t.Fatalf("refs %v", refs)
	}
}

// Moving a token from the shell to the keychain needs no new prepare.
func TestMapOverridesManifestAtUpWithoutPrepare(t *testing.T) {
	log := installFakeDocker(t)
	kc := fakeKeychain(t, map[string]string{"acme-npm-token": "keychain-canary-value-1"})
	_ = kc
	t.Setenv("SF_TEST_TOK", "shell-canary-value-1")
	root, stateDir, inst := secretRepo(t, "SF_TEST_NAME=env:SF_TEST_TOK")
	before, _ := os.ReadFile(filepath.Join(inst, "manifest.json"))

	mapSecret(t, root, "SF_TEST_NAME", "keychain:acme-npm-token")
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	if code != 0 {
		t.Fatalf("up failed: %s", stderr)
	}
	env := dockerEnvLog(log)
	if !strings.Contains(env, "SF_TEST_NAME=keychain-canary-value-1") || strings.Contains(env, "SF_TEST_NAME=shell-canary-value-1") {
		t.Fatal("compose must receive the keychain value, not the shell one")
	}
	after, _ := os.ReadFile(filepath.Join(inst, "manifest.json"))
	if string(before) != string(after) {
		t.Fatal("up must not rewrite the manifest")
	}
}

func TestMappedNameNeedsPrepareAndBareUpFails(t *testing.T) {
	installFakeDocker(t)
	fakeKeychain(t, map[string]string{"acme-new": "v"})
	root, stateDir, inst := secretRepo(t, "OLD=env:SF_TEST_OLD")
	branch, _ := currentBranch(root)
	rootResolved, _ := identity.ResolvePath(root)
	project, instanceDir, err := checkoutInstance(rootResolved, branch, stateDir, filepath.Join(t.TempDir(), "legacy"))
	if err != nil || instanceDir != inst {
		t.Fatalf("instance dir %q vs %q: %v", instanceDir, inst, err)
	}
	if needsPrepare(inst, rootResolved, branch, project) {
		t.Fatal("nothing changed: no prepare needed")
	}
	mapSecret(t, root, "NEWNAME", "keychain:acme-new")
	if !needsPrepare(inst, rootResolved, branch, project) {
		t.Fatal("a mapped name the manifest lacks needs a prepare")
	}
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--wait-timeout", "10")
	want := "secret NEWNAME is mapped but this instance was prepared without it; run stackfork start-env"
	if code != 1 || !strings.Contains(stderr, want) {
		t.Fatalf("bare up: code %d, stderr %q", code, stderr)
	}
}
