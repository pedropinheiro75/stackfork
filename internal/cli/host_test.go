package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

const hostFixture = `services:
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: "s3cret-literal"
    ports:
      - "5432:5432"
`

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func readManifestAt(t *testing.T, stateDir, project string) *state.Manifest {
	t.Helper()
	m, err := state.ReadManifest(filepath.Join(stateDir, project, "manifest.json"))
	if err != nil || m == nil {
		t.Fatalf("manifest: %v", err)
	}
	return m
}

func TestHostRunsWithEnvPortAndRoute(t *testing.T) {
	t.Setenv("STACKFORK_PORT_RANGE_START", "31000")
	t.Setenv("STACKFORK_PORT_RANGE_END", "31499")
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	out := filepath.Join(t.TempDir(), "env.txt")
	during := filepath.Join(t.TempDir(), "manifest-during.json")

	script := "env > '" + filepath.ToSlash(out) + "'; cat '" + filepath.ToSlash(filepath.Join(stateDir, project, "manifest.json")) + "' > '" + filepath.ToSlash(during) + "'"
	stdout, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", "sh", "-c", script)
	if code != 0 {
		t.Fatalf("host failed: code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "s3cret-literal") {
		t.Fatalf("a password must never be printed:\n%s%s", stdout, stderr)
	}
	envText, _ := os.ReadFile(out)
	m := readManifestAt(t, stateDir, project)
	port := m.HostPorts["app"]
	if port < 31000 || port > 31499 {
		t.Fatalf("expected a stable port in the test range persisted in the manifest, got %d", port)
	}
	for _, want := range []string{
		"PORT=" + strconv.Itoa(port), "NUXT_PORT=" + strconv.Itoa(port), "NITRO_PORT=" + strconv.Itoa(port),
		"HOST=127.0.0.1", "STACKFORK_HOST_RUN=1",
		"DATABASE_URL=postgres://postgres:s3cret-literal@127.0.0.1:20050/postgres", // same rule as `env --with-passwords`
	} {
		if !strings.Contains(string(envText), want) {
			t.Errorf("child env missing %q:\n%s", want, envText)
		}
	}
	var dm state.Manifest
	raw, _ := os.ReadFile(during)
	if err := json.Unmarshal(raw, &dm); err != nil || dm.Routes["app"] != port {
		t.Fatalf("route should be registered while the command runs: %v %+v", err, dm.Routes)
	}
	if _, still := m.Routes["app"]; still {
		t.Fatalf("route must be removed on exit: %+v", m.Routes)
	}

	// Same name, same port next time.
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", "true"); code != 0 {
		t.Fatalf("second host: %s", stderr)
	}
	if again := readManifestAt(t, stateDir, project).HostPorts["app"]; again != port {
		t.Fatalf("port should be stable: %d then %d", port, again)
	}
}

func TestHostExplicitPortAndExitCode(t *testing.T) {
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	port := freePort(t)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "web", "--port", strconv.Itoa(port), "--", "sh", "-c", "exit 7")
	if code != 7 {
		t.Fatalf("the command's exit code must be returned, got %d (%s)", code, stderr)
	}
	if got := readManifestAt(t, stateDir, project).HostPorts["web"]; got != port {
		t.Fatalf("explicit port not persisted: %d", got)
	}
}

func TestHostRefusals(t *testing.T) {
	root, stateDir, _, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"host", "app"}, "usage: host"},
		{[]string{"host", "--", "true"}, "usage: host"},
		{[]string{"host", "Bad_Name", "--", "true"}, "invalid name"},
		{[]string{"host", "app", "--", "definitely-not-a-command-xyz"}, "could not start"},
	} {
		_, stderr, code := runCLI(t, append([]string{"--root", root, "--state-dir", stateDir}, tc.args...)...)
		if code == 0 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: want failure containing %q, got code=%d %q", tc.args, tc.want, code, stderr)
		}
	}
	// A busy port is refused, not silently replaced.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--port", strconv.Itoa(l.Addr().(*net.TCPAddr).Port), "--", "true")
	if code == 0 || !strings.Contains(stderr, "already in use") {
		t.Fatalf("busy port: code=%d %q", code, stderr)
	}
}

func TestHostRefusesWhenNotRunning(t *testing.T) {
	root, stateDir, _, _ := envSetup(t, hostFixture) // DOCKER_FAKE_PS stays empty: not running
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", "true")
	if code == 0 || !strings.Contains(stderr, "not running") {
		t.Fatalf("code=%d %q", code, stderr)
	}
}

// The guard allows the sanctioned path even in a managed checkout.
func TestGuardAllowsHostRunPath(t *testing.T) {
	t.Setenv("STACKFORK_HOST_RUN", "")
	repo, _ := guardRepo(t)
	for _, command := range []string{
		"stackfork host app -- bun run dev",
		"cd . && stackfork host app --port 3000 -- npm run dev",
		"STACKFORK_HOST_RUN=1 bun run dev",
	} {
		var out, errBuf bytes.Buffer
		payload := `{"cwd":"` + jsonEsc(repo) + `","tool_name":"Bash","tool_input":{"command":"` + command + `"}}`
		if code, _ := cmdGuard(strings.NewReader(payload), &out, &errBuf, nil, t.TempDir(), t.TempDir()); code != 0 {
			t.Errorf("%q must be allowed, got code %d: %s", command, code, errBuf.String())
		}
	}
}

func TestCheckBatchArgs(t *testing.T) {
	cmdFile := func(string) (string, error) { return `C:\x\npm.CMD`, nil }
	exeFile := func(string) (string, error) { return `C:\x\node.exe`, nil }
	for _, bad := range []string{"a&b", "a|b", "a<b", "a>b", "a^b", "%PATH%", "hi!"} {
		if checkBatchArgs([]string{"npm", "run", bad}, cmdFile) == nil {
			t.Errorf("%q must be refused for a .cmd", bad)
		}
	}
	if checkBatchArgs([]string{"npm", "run", "dev", "--port", "3000"}, cmdFile) != nil {
		t.Error("plain arguments are fine")
	}
	if checkBatchArgs([]string{"node", "a&b"}, exeFile) != nil {
		t.Error("an .exe is not a batch file")
	}
	if checkBatchArgs([]string{"nope", "a&b"}, func(string) (string, error) { return "", errors.New("x") }) != nil {
		t.Error("unresolved commands are left to exec")
	}
}

// The block's lines come after the inherited environment, so they win
// (exec.Cmd keeps the last of a repeated name); extras come last.
func TestHostEnvironOrder(t *testing.T) {
	t.Setenv("DATABASE_URL", "inherited")
	env := hostEnviron("DATABASE_URL=from-block\nnot a line\n=skipped\nREDIS_URL=r")
	last := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		last[k] = v
	}
	if last["DATABASE_URL"] != "from-block" || last["REDIS_URL"] != "r" || last["STACKFORK_HOST_RUN"] != "1" {
		t.Fatalf("last values %v", last)
	}
	if env[len(env)-1] != "STACKFORK_HOST_RUN=1" {
		t.Fatalf("extras must come last: %v", env[len(env)-1])
	}
	if _, ok := last[""]; ok {
		t.Fatal("a line without a name is dropped")
	}
}
