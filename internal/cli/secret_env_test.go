package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// D9: a Start resolves a secret, the compose child gets it under the name and
// every alias, and a host step launched afterwards inherits nothing of it.
func TestSecretValueReachesOnlyTheComposeChild(t *testing.T) {
	const canary = "env-canary-value-4-2"
	root, state := reposEnv(t)
	repo := planRepo(t, root, runSteps)
	log := installFakeDocker(t)
	fakeKeychain(t, map[string]string{"acme-npm-token": canary})
	mapSecret(t, repo, "npm_token", "keychain:acme-npm-token")
	var hostEnv []string
	old := lifecycle.HostStart
	lifecycle.HostStart = func(out, _ io.Writer, _ []string, _, _ string, s knowledge.Step) error {
		hostEnv = os.Environ() // what a host child inherits from stackfork
		fmt.Fprintf(out, "started %s\n", s.Name)
		return nil
	}
	defer func() { lifecycle.HostStart = old }()
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	runCLI(t, "config", "set-mode", repo, "hybrid")
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 || strings.Contains(out+e, canary) {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if hostEnv == nil {
		t.Fatal("the host step did not run")
	}
	for _, kv := range hostEnv {
		if strings.Contains(kv, canary) {
			t.Fatalf("a host child would inherit the value: %s", kv[:strings.Index(kv, "=")])
		}
	}
	for _, name := range []string{"npm_token", "NPM_TOKEN"} {
		if os.Getenv(name) != "" {
			t.Fatalf("%s is set in stackfork's own environment", name)
		}
	}
	env := dockerEnvLog(log)
	for _, kv := range []string{"npm_token=" + canary, "NPM_TOKEN=" + canary} {
		if !strings.Contains(env, "\n"+kv+"\n") {
			t.Fatalf("the compose child lacks %s", kv[:strings.Index(kv, "=")])
		}
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), canary) {
		t.Fatal("the value is in a docker argument list")
	}
}

// Outside a run, `run SERVICE` resolves the map's reference itself.
func TestRunResolvesMappedSecretWithoutLeakingIt(t *testing.T) {
	log := installFakeDocker(t)
	fakeKeychain(t, map[string]string{"acme-run": "run-canary-value"})
	root, stateDir, _ := secretRepo(t, "SF_RUN_TOK=env:SF_TEST_RUN_UNSET")
	mapSecret(t, root, "SF_RUN_TOK", "keychain:acme-run")
	if _, e, code := runCLI(t, "--root", root, "--state-dir", stateDir, "run", "db", "true"); code != 0 {
		t.Fatal(e)
	}
	if !strings.Contains(dockerEnvLog(log), "\nSF_RUN_TOK=run-canary-value\n") || os.Getenv("SF_RUN_TOK") != "" {
		t.Fatal("run must pass the value to the compose child only")
	}
}
