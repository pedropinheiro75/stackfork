package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A service can reference a named secret directly, `NPM_TOKEN: ${NPM_TOKEN}`,
// with no Compose `secrets:` block involved - the exact pattern 253fb81
// broke (an unresolved ${NPM_TOKEN} baked in as a literal empty string,
// leaving --secret/--npmrc's real value with nothing left in the snapshot to
// interpolate into at up time).
const npmTokenRefCompose = `services:
  app:
    image: alpine:3.20
    command: ["sh", "-c", "echo GOT_NPM_TOKEN=$NPM_TOKEN && sleep 300"]
    environment:
      NPM_TOKEN: ${NPM_TOKEN}
`

// design.md D6, 253fb81 regression: the rendered snapshot must keep the
// literal "${NPM_TOKEN}" reference (never empty - the regression - and
// never the ambient ${NPM_TOKEN} ${SF_TEST...} secret's real value - the
// original leak).
func TestSecretReferencedDirectlyKeepsLiteralInSnapshot(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(npmTokenRefCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")

	const ambientCanary = "ambient-npm-token-must-never-leak"
	t.Setenv("NPM_TOKEN", ambientCanary) // mimics a shell that already exports NPM_TOKEN
	t.Setenv("SF_TEST_NPM_TOKEN_SOURCE", "resolved-only-at-up-not-prepare")

	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir,
		"--secret", "npm_token=env:SF_TEST_NPM_TOKEN_SOURCE", "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, out)
	}
	stateDirOut := prepared["state"].(string)

	configData, err := os.ReadFile(filepath.Join(stateDirOut, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), ambientCanary) {
		t.Fatal("snapshot leaked the ambient NPM_TOKEN value")
	}
	if !strings.Contains(string(configData), `"NPM_TOKEN": "${NPM_TOKEN}"`) {
		t.Fatalf("expected snapshot to keep the literal ${NPM_TOKEN} reference, got:\n%s", configData)
	}
}

// Live regression test: `up` must give the actual container the real
// resolved secret (via --secret, exactly as design.md D6 always intended),
// and it must never land in compose.json or manifest.json.
func TestLiveSecretReferencedDirectlyResolvesAtUp(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)

	const realToken = "live-secret-directly-referenced-9f2e"
	t.Setenv("SF_TEST_LIVE_NPM_TOKEN", realToken)

	before := snapshotDocker(t)
	scratch := t.TempDir()
	root := filepath.Join(scratch, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "-q", root)
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(npmTokenRefCompose), 0o600); err != nil {
		t.Fatal(err)
	}

	state := filepath.Join(scratch, "state")
	li := liveInstance{binary: bin, root: root, state: state}
	defer exec.Command(li.binary, li.cli("down", "--volumes")...).Run() //nolint:errcheck // best-effort cleanup on any exit path

	prepareOut := li.run(t, "--secret", "npm_token=env:SF_TEST_LIVE_NPM_TOKEN", "prepare", "-f", filepath.Join(root, "compose.yaml"))
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(prepareOut), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, prepareOut)
	}
	project, _ := prepared["project"].(string)
	instanceDir := filepath.Join(state, project)

	configData, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configData), realToken) {
		t.Fatalf("compose.json contains the real secret value:\n%s", configData)
	}
	if !strings.Contains(string(configData), `"NPM_TOKEN": "${NPM_TOKEN}"`) {
		t.Fatalf("expected compose.json to keep the literal ${NPM_TOKEN} reference:\n%s", configData)
	}

	upOut := li.run(t, "up", "--wait-timeout", "60")
	if strings.Contains(upOut, realToken) {
		t.Fatalf("up's own stdout leaked the secret value:\n%s", upOut)
	}

	// `logs` redacts a known secret value (task 4.1a, design.md D5) before
	// it ever reaches this test's/the user's own output - the marker
	// proves the container really got the resolved value (redaction only
	// replaces a value it actually found in the stream), without this test
	// itself having to handle the raw secret past this point.
	logsOut := li.run(t, "logs", "app", "--tail", "200")
	if strings.Contains(logsOut, realToken) {
		t.Fatalf("logs output leaked the raw secret value instead of redacting it:\n%s", logsOut)
	}
	if !strings.Contains(logsOut, "GOT_NPM_TOKEN=[redacted:npm_token]") {
		t.Fatalf("expected the running container to have received the real resolved secret (redacted in logs output), got:\n%s", logsOut)
	}

	manifestData, err := os.ReadFile(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifestData), realToken) {
		t.Fatalf("manifest.json contains the real secret value:\n%s", manifestData)
	}

	li.run(t, "down", "--volumes")
	after := snapshotDocker(t)
	assertSameInventory(t, before, after)
}

// design.md D6: any ${VAR} Render could not resolve at all (not a named
// secret, not in --env-file, no `:-default` in the source) gets a one-line
// warning naming it, so a silent empty is never the only signal - the
// coordinator's account of how this bit app-d in the first place: NPM_TOKEN
// quietly resolved to "" with nothing printed anywhere.
func TestPrepareWarnsOnUnresolvedVariable(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	const compose = `services:
  app:
    image: alpine:3.20
    command: ["sh", "-c", "sleep 300"]
    environment:
      MISSING: ${SF_TEST_TRULY_UNRESOLVED_VAR}
      HAS_DEFAULT: ${SF_TEST_TRULY_UNRESOLVED_VAR2:-fine}
`
	if err := os.WriteFile(composePath, []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	os.Unsetenv("SF_TEST_TRULY_UNRESOLVED_VAR")
	os.Unsetenv("SF_TEST_TRULY_UNRESOLVED_VAR2")

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare failed: %s", stderr)
	}
	if !strings.Contains(stderr, "SF_TEST_TRULY_UNRESOLVED_VAR") {
		t.Fatalf("expected a warning naming the unresolved variable, got stderr: %q", stderr)
	}
	if strings.Contains(stderr, "SF_TEST_TRULY_UNRESOLVED_VAR2") {
		t.Fatalf("a variable with its own default must not be warned about: %q", stderr)
	}
}
