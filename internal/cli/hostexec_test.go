package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	_, cwd, _ := strings.Cut(out, "cwd=")
	cwd, _, _ = strings.Cut(cwd, "\n")
	a, _ := os.Stat(cwd)
	b, _ := os.Stat(root) // the same folder; Windows may print its short (8.3) name
	if a == nil || b == nil || !os.SameFile(a, b) || !strings.Contains(out, "stdin=true") {
		t.Fatalf("cwd or stdin:\n%s", out)
	}
	if !strings.Contains(out, "DATABASE_URL=postgres") || !strings.Contains(out, "STACKFORK_HOST_RUN=1") || strings.Contains(out, "PORT=3") {
		t.Fatalf("environment:\n%s", out)
	}
}

// The child's stdin is closed even when stackfork's own stdin is a pipe with data.
func TestHostExecStdinIsClosedWhenTheParentHasInput(t *testing.T) {
	_, _, _, run := hostExecEnv(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString("input the step must not read\n")
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close(); w.Close() })
	out, code, err := run(helperStep("", "a"))
	if err != nil || code != 0 || !strings.Contains(out, "stdin=true") {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
}

// A secret that a keychain or op reference resolves to is never in the host
// command's environment; only stackfork's own environment and the block are.
func TestHostExecEnvironmentHasNoSecretValue(t *testing.T) {
	const canary = "host-secret-canary-value-1"
	fakeKeychain(t, map[string]string{"acme-host-secret": canary})
	root, _, _, run := hostExecEnv(t)
	mapSecret(t, root, "SF_HOST_SECRET", "keychain:acme-host-secret")
	t.Setenv("HELPER_DUMP_ENV", "1")
	out, code, err := run(helperStep("", "a"))
	if err != nil || code != 0 || !strings.Contains(out, "env=") {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
	if strings.Contains(out, canary) || strings.Contains(out, "SF_HOST_SECRET") {
		t.Fatalf("a secret reached the host command:\n%s", out)
	}
}

// A grandchild that keeps stdout open must not hold the run (and its instance
// lock) after the command itself exited.
func TestHostExecDoesNotWaitForAGrandchildHoldingOutput(t *testing.T) {
	_, _, _, run := hostExecEnv(t)
	t.Setenv("HELPER_BACKGROUND", "1")
	old := hostExecWaitDelay
	hostExecWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { hostExecWaitDelay = old })
	start := time.Now()
	out, code, err := run(helperStep("", "a"))
	if err != nil || code != 0 || !strings.Contains(out, "parent done") {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited %v for the grandchild", d)
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
	if err := os.MkdirAll(filepath.Join(filepath.Dir(root), "other"), 0o755); err != nil { // it exists: only the confinement can refuse it
		t.Fatal(err)
	}
	for _, dir := range []string{"..", "../other", "escape"} {
		out, _, err := run(helperStep(dir, "a"))
		if err == nil || !strings.Contains(err.Error(), "outside the checkout") {
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

// On Windows a .cmd or .bat target runs through cmd.exe: a metacharacter in
// its arguments is refused before anything starts. Every OS runs this check
// with the OS and the lookup injected.
func TestHostExecWindowsRefusesBatchMetacharacters(t *testing.T) {
	cmdFile := func(string) (string, error) { return `C:\proj\node_modules\.bin\migrate.cmd`, nil }
	exeFile := func(string) (string, error) { return `C:\Program Files\bun\bun.exe`, nil }
	for _, c := range []string{"&", "|", "<", ">", "^", "%", "!"} {
		err := checkHostArgvFor("windows", []string{"migrate.cmd", "up" + c + "x"}, cmdFile)
		if err == nil || !strings.Contains(err.Error(), "cmd.exe") {
			t.Errorf("%q: %v", c, err)
		}
	}
	argv := []string{"migrate.cmd", "up%PATH%"}
	if err := checkHostArgvFor("windows", argv, cmdFile); err == nil || !strings.Contains(err.Error(), `"up%PATH%"`) {
		t.Fatalf("batch file with %%: %v", err)
	}
	if checkHostArgvFor("linux", argv, cmdFile) != nil || checkHostArgvFor("windows", []string{"bun", "run", "a&b"}, exeFile) != nil {
		t.Fatal("only a batch target on Windows is checked")
	}
	// a quoted program path with spaces stays one argument
	got, err := knowledge.SplitArgv(`"C:\Program Files\bun\bun.exe" run db:migrate`)
	if err != nil || len(got) != 3 || got[0] != `C:\Program Files\bun\bun.exe` {
		t.Fatalf("%q %v", got, err)
	}
}
