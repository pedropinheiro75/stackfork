package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// hostExecEnv prepares a running database-only instance and returns a
// function that runs a host exec step against it with the helper command.
func hostExecEnv(t *testing.T) (root, stateDir, project string, run func(step knowledge.Step) (string, int, error)) {
	t.Helper()
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	t.Setenv("STACKFORK_TEST_HELPER", "1")
	run = func(step knowledge.Step) (string, int, error) {
		var out bytes.Buffer
		code, err := lifecycle.HostExec(&out, project, root, filepath.Join(stateDir, project), stateDir, t.TempDir(), step)
		return out.String(), code, err
	}
	return
}

func helperStep(dir string, args string) knowledge.Step {
	return knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnHost, Dir: dir, Cmd: os.Args[0] + " " + args}
}

func TestHostExecRunsArgvWithoutShellInTheCheckout(t *testing.T) {
	root, _, _, run := hostExecEnv(t)
	out, code, err := run(helperStep("", `"two words" '*' x`))
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
	if !strings.Contains(out, `argv=["two words","*","x"]`) {
		t.Fatalf("argv not passed as written:\n%s", out)
	}
	rootR, _ := filepath.EvalSymlinks(root)
	if !strings.Contains(out, "cwd="+rootR+"\n") || !strings.Contains(out, "stdin=true") {
		t.Fatalf("cwd or stdin:\n%s", out)
	}
	if !strings.Contains(out, "DATABASE_URL=postgres") || !strings.Contains(out, "STACKFORK_HOST_RUN=1") || strings.Contains(out, "PORT=3") {
		t.Fatalf("environment:\n%s", out)
	}
}

func TestHostExecDirConfinedToTheCheckout(t *testing.T) {
	root, _, _, run := hostExecEnv(t)
	if err := os.MkdirAll(filepath.Join(root, "apps", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	out, code, err := run(helperStep("apps/api", "a"))
	if err != nil || code != 0 || !strings.Contains(out, filepath.Join("apps", "api")+"\n") {
		t.Fatalf("inside: code %d err %v\n%s", code, err, out)
	}
	for _, dir := range []string{"..", "../other", "escape"} {
		out, _, err := run(helperStep(dir, "a"))
		if err == nil || !strings.Contains(err.Error(), "outside the checkout") && !strings.Contains(err.Error(), "no such file") {
			t.Errorf("dir %q: err %v", dir, err)
		}
		if strings.Contains(out, "argv=") {
			t.Errorf("dir %q: the command ran", dir)
		}
	}
	if _, _, err := run(helperStep("escape", "a")); err == nil || !strings.Contains(err.Error(), "outside the checkout") {
		t.Errorf("symlink escape: %v", err)
	}
}

func TestHostExecExitStatusAndOutput(t *testing.T) {
	_, _, _, run := hostExecEnv(t)
	t.Setenv("STACKFORK_TEST_HELPER_EXIT", "3")
	t.Setenv("HELPER_PRINT", "visible")
	out, code, err := run(helperStep("", "a"))
	if err != nil || code != 3 {
		t.Fatalf("code %d err %v", code, err)
	}
	if !strings.Contains(out, "stderr visible") || !strings.Contains(out, "HELPER_PRINT=visible") {
		t.Fatalf("stdout and stderr both reach the writer:\n%s", out)
	}
	if _, code, err := run(knowledge.Step{ID: "m", Kind: knowledge.KindExec, On: knowledge.OnHost, Cmd: "stackfork-no-such-program"}); err == nil || code != 0 {
		t.Fatalf("a missing program is an error: %d %v", code, err)
	}
}

func TestHostExecFailsWhenTheInstanceEnvironmentIsUnavailable(t *testing.T) {
	root, stateDir, project, _ := hostExecEnv(t)
	t.Setenv("DOCKER_FAKE_PS", "") // not running
	var out bytes.Buffer
	_, err := lifecycle.HostExec(&out, project, root, filepath.Join(stateDir, project), stateDir, t.TempDir(), helperStep("", "a"))
	if err == nil || strings.Contains(out.String(), "argv=") {
		t.Fatalf("err %v out %q", err, out.String())
	}
}
