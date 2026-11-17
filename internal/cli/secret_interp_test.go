package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// A service that lists no secret and reads the source variable of one through
// ${NAME} gets an empty value. prepare says so and names the fix.
const interpSecretCompose = "services:\n  app:\n    image: alpine\n    secrets: [esec]\n  interp:\n    image: alpine\n    environment:\n      INTERP: \"${SF_TEST_INTERP_VAR}\"\nsecrets:\n  esec:\n    environment: SF_TEST_INTERP_VAR\n"

func interpPrepare(t *testing.T) (stderr, snapshot string) {
	t.Helper()
	testsupport.RequireCompose(t)
	root, stateDir, _ := secretRepo(t)
	path := filepath.Join(root, "interp.compose.yaml")
	os.WriteFile(path, []byte(interpSecretCompose), 0o600)
	out, e, code := runCLI(t, "--root", root, "--state-dir", stateDir, "--secret", "esec=env:SF_TEST_INTERP_VAR", "prepare", "-f", path)
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	ms, _ := filepath.Glob(filepath.Join(stateDir, "*", "compose.json"))
	if len(ms) != 1 {
		t.Fatalf("snapshot not found: %s", out)
	}
	b, _ := os.ReadFile(ms[0])
	return e, string(b)
}

func TestPrepareNamesTheFixForASecretSourceReadThroughInterpolation(t *testing.T) {
	os.Unsetenv("SF_TEST_INTERP_VAR")
	e, _ := interpPrepare(t)
	for _, want := range []string{
		"defaulted to empty: SF_TEST_INTERP_VAR",
		"SF_TEST_INTERP_VAR is the source of secret esec",
		"list esec under secrets:",
		"--secret SF_TEST_INTERP_VAR=env:SF_TEST_INTERP_VAR",
	} {
		if !strings.Contains(e, want) {
			t.Fatalf("missing %q in:\n%s", want, e)
		}
	}
}

// The value of a variable set in the shell that runs prepare never reaches the
// snapshot, the output or the hint.
func TestPrepareNeverWritesTheSourceVariableValue(t *testing.T) {
	const canary = "interp-canary-value-7788"
	t.Setenv("SF_TEST_INTERP_VAR", canary)
	e, snap := interpPrepare(t)
	if strings.Contains(snap, canary) || strings.Contains(e, canary) {
		t.Fatalf("the value was written:\n%s\n%s", e, snap)
	}
}

func TestSecretSourceHintsIgnoresVariablesThatNoSecretReads(t *testing.T) {
	got := secretSourceHints([]string{"OTHER", "SRC"}, map[string]string{"a": "SRC", "b": "SRC", "c": ""})
	if len(got) != 1 || !strings.Contains(got[0], "secret a, b") {
		t.Fatalf("%v", got)
	}
	if len(secretSourceHints([]string{"OTHER"}, map[string]string{"a": "SRC"})) != 0 {
		t.Fatal("a hint for an unrelated variable")
	}
}

// An `up` over a running container that lists a secret says that the old value
// stays until stop and start. It never prints a value.
func TestUpNotesThatARunningServiceKeepsItsSecretValue(t *testing.T) {
	t.Setenv("SF_TEST_START_QUIET", "note-canary-value")
	repo, base, _ := startSecretProject(t, "npm_token=env:SF_TEST_START_QUIET")
	args := []string{"--root", repo, "--state-dir", base[1], "--legacy-state-dir", base[3], "up", "--wait-timeout", "10"}

	t.Setenv("DOCKER_FAKE_PS", "")
	if out, e, _ := runCLI(t, args...); strings.Contains(out+e, "keeps the secret values") {
		t.Fatalf("a note without a running container: %s %s", out, e)
	}
	t.Setenv("DOCKER_FAKE_PS", "abc123")
	out, e, _ := runCLI(t, args...)
	if !strings.Contains(e, "note: app already runs and keeps the secret values it started with") ||
		!strings.Contains(e, "stackfork stop app && stackfork start app") || strings.Contains(out+e, "note-canary-value") {
		t.Fatalf("%s %s", out, e)
	}
}
