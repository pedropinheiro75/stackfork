package dashboard_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// startSecretFixture prepares an instance with tok=env:VAR and puts a docker
// wrapper in front that records the environment of each `compose start`.
func startSecretFixture(t *testing.T, varName string) (srv *dashboard.Server, project, envLog string) {
	t.Helper()
	newBase, legacyBase, project := preparedFixture(t)
	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	var out, errBuf bytes.Buffer
	if code := cli.Run([]string{"--root", m.Root, "--state-dir", newBase, "--secret", "tok=env:" + varName,
		"prepare", "--force", "-f", filepath.Join(m.Root, "compose.yaml")}, &out, &errBuf); code != 0 {
		t.Fatal(errBuf.String())
	}
	real, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("no docker")
	}
	dir := t.TempDir()
	envLog = filepath.Join(dir, "start.env")
	testsupport.FakeExe(t, filepath.Join(dir, "docker"), `#!/bin/sh
case " $* " in
  *" compose "*" start "*|"compose "*" start "*) { echo "--- $*"; env; } >> "`+envLog+`" ;;
esac
exec "`+filepath.ToSlash(real)+`" "$@"
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv, err = dashboard.New(newBase, legacyBase, "test", 55421)
	if err != nil {
		t.Fatal(err)
	}
	return srv, project, envLog
}

// Review I1: the dashboard's per-service Start resolves in a secrets step
// first and hands the values to Compose, which gets them and nothing else.
func TestServiceStartPassesTheSecretToTheComposeChildOnly(t *testing.T) {
	const val = "svc-start-canary-5-1"
	t.Setenv("SF_TEST_SVC_START", val)
	srv, project, envLog := startSecretFixture(t, "SF_TEST_SVC_START")
	code, resp := postAction(t, srv, "127.0.0.1:55421", actionRequestFixture{Action: "service-start", Project: project, Service: "db"})
	if code != 200 || !resp.OK || strings.Contains(resp.Message, val) {
		t.Fatalf("%d %+v", code, resp)
	}
	b, _ := os.ReadFile(envLog)
	if !strings.Contains(string(b), "\ntok="+val+"\n") {
		t.Fatalf("the compose child lacks the value:\n%s", b)
	}
	if os.Getenv("tok") != "" {
		t.Fatal("tok is set in stackfork's own environment")
	}
}

// An unresolvable reference fails with the dashboard's message before any
// Compose start.
func TestServiceStartFailsBeforeComposeWhenASecretDoesNotResolve(t *testing.T) {
	srv, project, envLog := startSecretFixture(t, "SF_TEST_SVC_START_UNSET")
	code, resp := postAction(t, srv, "127.0.0.1:55421", actionRequestFixture{Action: "service-start", Project: project, Service: "db"})
	if code != 200 || resp.OK || !strings.Contains(resp.Message, "SF_TEST_SVC_START_UNSET") || !strings.Contains(resp.Message, "does not see your shell environment") {
		t.Fatalf("%d %+v", code, resp)
	}
	if _, err := os.Stat(envLog); err == nil {
		t.Fatal("compose start ran")
	}
}
