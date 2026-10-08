package dashboard_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// installFakeDocker mirrors the one every other package's tests use: a
// `docker` first on PATH that delegates `compose ... config` to the real
// docker (so prepare renders a genuine snapshot) and returns canned/empty
// output for everything else, so this test needs no daemon.
//   - `docker ps --filter label=<legacy root label>=...` -> $DOCKER_FAKE_PS_LEGACY (empty by default)
//   - `docker ps ...`                                    -> $DOCKER_FAKE_PS (empty by default)
func installFakeDocker(t *testing.T) {
	t.Helper()
	real, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not available to delegate `compose config` to")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
[ -n "$DOCKER_FAKE_LOG" ] && echo "$*" >> "$DOCKER_FAKE_LOG"
if [ "$1" = "volume" ] && [ "$2" = "inspect" ]; then exit "${DOCKER_FAKE_VOLUME_EXIT:-0}"; fi
if [ "$1" = "compose" ]; then
  for a in "$@"; do
    if [ "$a" = "config" ]; then
      exec "` + real + `" "$@"
    fi
  done
  if [ -n "$DOCKER_FAKE_SLEEP" ]; then
    sleep "$DOCKER_FAKE_SLEEP"
  fi
  case " $* " in
    *" logs "*) printf '%s' "$DOCKER_FAKE_LOGS" ;;
  esac
  exit "${DOCKER_FAKE_EXIT:-0}"
fi
if [ "$1" = "context" ] && [ "$2" = "inspect" ]; then
  printf '%s' '[{"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock"}}}]'
  exit 0
fi
if [ "$1" = "ps" ]; then
  [ -n "$DOCKER_FAKE_PS_SLEEP" ] && sleep "$DOCKER_FAKE_PS_SLEEP"
  case " $* " in
    *"` + identity.LegacyLabelRoot + `"*) printf '%s' "$DOCKER_FAKE_PS_LEGACY" ;;
    *) printf '%s' "$DOCKER_FAKE_PS" ;;
  esac
  exit 0
fi
if [ "$1" = "inspect" ]; then
  printf 'false'
  exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const fixtureCompose = "services:\n  db:\n    image: postgres:17\n    ports:\n      - \"5432:5432\"\n"

// TestInstancesMatchesListJSON is task 2.1's golden test (design.md D1):
// `list --json` and GET /api/instances must describe the same machine state
// identically, for the same fixture.
func TestInstancesMatchesListJSON(t *testing.T) {
	installFakeDocker(t)

	oldVersion := cli.Version
	cli.Version = "golden-test-version"
	defer func() { cli.Version = oldVersion }()

	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	legacyStateDir := filepath.Join(t.TempDir(), "legacy-state")

	var prepOut, stderrBuf strings.Builder
	code := cli.Run([]string{"--root", root, "--state-dir", stateDir, "prepare", "-f", composePath}, &prepOut, &stderrBuf)
	if code != 0 {
		t.Fatalf("prepare failed: code=%d stderr=%s", code, stderrBuf.String())
	}

	var listOut, listErr strings.Builder
	code = cli.Run([]string{"--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "list", "--json"}, &listOut, &listErr)
	if code != 0 {
		t.Fatalf("list --json failed: code=%d stderr=%s", code, listErr.String())
	}

	srv, err := dashboard.New(stateDir, legacyStateDir, "golden-test-version", 54321)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Host = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /api/instances: status %d, body %s", rec.Code, rec.Body.String())
	}

	var fromList, fromAPI interface{}
	if err := json.Unmarshal([]byte(listOut.String()), &fromList); err != nil {
		t.Fatalf("list --json output not JSON: %v\n%s", err, listOut.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fromAPI); err != nil {
		t.Fatalf("/api/instances output not JSON: %v\n%s", err, rec.Body.String())
	}
	if !reflect.DeepEqual(fromList, fromAPI) {
		t.Fatalf("list --json and /api/instances disagree:\nlist: %s\napi:  %s", listOut.String(), rec.Body.String())
	}
}
