package cli

import (
	"fmt"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"io"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// D11: the run redactor uses the preflight's values, so an op:// value a step
// prints is redacted in the terminal output, the step log and the stream.
func TestOPValuePrintedByAStepIsRedactedEverywhere(t *testing.T) {
	const canary = "op-canary-value-4-3-xyz"
	root, state := reposEnv(t)
	repo := planRepo(t, root, []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "hello", Kind: knowledge.KindNote, Text: "the key is " + canary},
	})
	fakeOp(t, canary)
	mapSecret(t, repo, "STRIPE_TEST_KEY", "op://Dev/acme-stripe/test-key")
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if strings.Contains(out+e, canary) || !strings.Contains(out, "[redacted:STRIPE_TEST_KEY]") {
		t.Fatalf("terminal output:\n%s%s", out, e)
	}
	dir := runsInstance(t, state)
	b, err := runs.Log(dir, 1, "hello")
	if err != nil || strings.Contains(string(b), canary) || !strings.Contains(string(b), "[redacted:STRIPE_TEST_KEY]") {
		t.Fatalf("step log %q %v", b, err)
	}
}

// A host exec step writes to the run's redacting step writer: a secret value
// its command prints is redacted in the terminal output and the step log.
func TestHostExecOutputIsRedacted(t *testing.T) {
	const canary = "op-canary-host-exec-9"
	root, state := reposEnv(t)
	repo := planRepo(t, root, hostExecSteps)
	fakeOp(t, canary)
	mapSecret(t, repo, "STRIPE_TEST_KEY", "op://Dev/acme-stripe/test-key")
	old := lifecycle.HostExec
	lifecycle.HostExec = func(w io.Writer, _, _, _, _, _ string, s knowledge.Step) (int, error) {
		fmt.Fprintln(w, "migrating with key "+canary)
		return 0, nil
	}
	defer func() { lifecycle.HostExec = old }()
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if strings.Contains(out+e, canary) || !strings.Contains(out, "[redacted:STRIPE_TEST_KEY]") {
		t.Fatalf("terminal output:\n%s%s", out, e)
	}
	b, err := runs.Log(runsInstance(t, state), 1, "migrate")
	if err != nil || strings.Contains(string(b), canary) || !strings.Contains(string(b), "[redacted:STRIPE_TEST_KEY]") {
		t.Fatalf("step log %q %v", b, err)
	}
}
