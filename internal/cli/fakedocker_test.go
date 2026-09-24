package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// installFakeDocker puts a fake `docker` first on PATH for the duration of
// the test. It records every invocation (one line per call, args space
// joined) to logPath, delegates `docker compose ... config ...` to the real
// docker (so `prepare` still produces a genuine snapshot), and otherwise
// returns canned output without touching a real daemon:
//   - `docker context inspect` -> a local unix-socket context
//   - `docker ps --filter label=<legacy root label>=...` -> $DOCKER_FAKE_PS_LEGACY (empty by default)
//   - `docker ps ...`          -> $DOCKER_FAKE_PS (empty by default)
//   - `docker compose ... ps --format json` -> $DOCKER_FAKE_STATUS (empty by default)
//   - anything else -> succeeds silently
func installFakeDocker(t *testing.T) (logPath string) {
	t.Helper()
	real, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not available to delegate `compose config` to")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "log.txt")
	script := `#!/bin/sh
{ for a in "$@"; do printf '%s ' "$a"; done; printf '\n'; } >> "` + logPath + `"
if [ "$1" = "compose" ]; then
  for a in "$@"; do
    if [ "$a" = "config" ]; then
      exec "` + real + `" "$@"
    fi
  done
  case " $* " in
    *" ps "*) printf '%s' "$DOCKER_FAKE_STATUS" ;;
  esac
  exit "${DOCKER_FAKE_EXIT:-0}"
fi
if [ "$1" = "context" ] && [ "$2" = "inspect" ]; then
  printf '%s' '[{"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock"}}}]'
  exit 0
fi
if [ "$1" = "ps" ]; then
  case " $* " in
    *"` + identity.LegacyLabelRoot + `"*) printf '%s' "$DOCKER_FAKE_PS_LEGACY" ;;
    *) printf '%s' "$DOCKER_FAKE_PS" ;;
  esac
  exit 0
fi
exit 0
`
	fake := filepath.Join(dir, "docker")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}
