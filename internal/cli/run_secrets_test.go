package cli

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func runServiceCLI(t *testing.T, repo string, base []string, args ...string) (string, string, int) {
	t.Helper()
	return runCLI(t, append([]string{"--root", repo, "--state-dir", base[1], "--legacy-state-dir", base[3], "run"}, args...)...)
}

// `run SERVICE` runs the same secrets step as `start SERVICE`: a name the
// Compose file uses and nothing supplies fails with the fix, before any
// Compose call.
func TestRunServiceThatListsASecretFailsAtTheSecretsStep(t *testing.T) {
	repo, base, dockerLog := startSecretProject(t)
	_, e, code := runServiceCLI(t, repo, base, "app", "true")
	if code == 0 || !strings.Contains(e, "npm_token is used by app.compose.yaml but has no reference; run: stackfork secret map") {
		t.Fatalf("%d %s", code, e)
	}
	noDockerCalls(t, dockerLog)
}

// `run worker` needs no values when the service lists no secret.
func TestRunServiceWithoutSecretsIgnoresAnUnresolvedSecret(t *testing.T) {
	repo, base, dockerLog := startSecretProject(t)
	out, e, code := runServiceCLI(t, repo, base, "worker", "true")
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	if b, _ := os.ReadFile(dockerLog); !strings.Contains(string(b), " run --rm --no-deps -T worker true") {
		t.Fatalf("compose run did not run:\n%s", b)
	}
	if strings.Contains(out+e, "has no reference") {
		t.Fatalf("the secrets lines are printed: %s %s", out, e)
	}
}

// A resolved reference reaches the Compose child of `run`, and the step prints
// nothing when it succeeds.
func TestRunServiceWithAResolvedSecretPassesItToCompose(t *testing.T) {
	t.Setenv("SF_TEST_RUN_STEP", "run-step-canary")
	repo, base, dockerLog := startSecretProject(t, "npm_token=env:SF_TEST_RUN_STEP")
	out, e, code := runServiceCLI(t, repo, base, "app", "true")
	if code != 0 || strings.Contains(out+e, "run-step-canary") || strings.Contains(out+e, "resolves") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	// Windows names are case-insensitive: the child keeps the last alias.
	name := "npm_token"
	if runtime.GOOS == "windows" {
		name = "NPM_TOKEN"
	}
	if !strings.Contains(dockerEnvLog(dockerLog), "\n"+name+"=run-step-canary\n") {
		t.Fatal("the compose child lacks the value")
	}
}
