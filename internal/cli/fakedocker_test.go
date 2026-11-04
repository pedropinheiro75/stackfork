package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// installFakeDocker puts a fake `docker` first on PATH for the duration of
// the test. It records every invocation (one line per call, args space
// joined) to logPath, delegates `docker compose ... config ...` to the real
// docker (so `prepare` still produces a genuine snapshot), and otherwise
// returns canned output without touching a real daemon:
//   - `docker context inspect` -> a local unix-socket context
//   - `docker ps --filter label=<legacy root label>=...` -> $DOCKER_FAKE_PS_LEGACY (empty by default)
//   - `docker ps ...`          -> $DOCKER_FAKE_PS (empty by default); `docker ps -a ...` -> $DOCKER_FAKE_PS_ALL (else $DOCKER_FAKE_PS)
//   - `docker compose ... ps --format json` -> $DOCKER_FAKE_STATUS (empty by default)
//   - `docker compose ... logs ...`         -> $DOCKER_FAKE_LOGS (empty by default)
//   - `docker compose ... exec|run ...`     -> $DOCKER_FAKE_EXEC_OUT (empty by default)
//   - `docker volume ls ...`   -> $DOCKER_FAKE_VOLUMES, one name per line (empty by default)
//   - `docker inspect -f {{.State.Running}} ...` -> $DOCKER_FAKE_PROXY_RUNNING (false by default)
//   - `docker inspect -f {{(index ...HostPort}} ...` -> $DOCKER_FAKE_PROXY_PORT (empty by default)
//   - `docker inspect -f {{.HostConfig.NetworkMode}} ...` -> $DOCKER_FAKE_PROXY_NETMODE (bridge by default), exit 1 if $DOCKER_FAKE_PROXY_MISSING is set
//   - `docker network ls ...` -> $DOCKER_FAKE_NETWORKS, one name per line (empty by default)
//   - `docker network inspect ...` -> $DOCKER_FAKE_NET_ENDPOINTS, one endpoint name per line (empty by default)
//   - `docker container inspect -f {{.State.Running}} NAME` -> true when NAME is in $DOCKER_FAKE_CONTAINERS (space separated), else exit 1
//   - `docker compose ... up ...` -> fails with $DOCKER_FAKE_UP_MSG on stderr once (every time when $DOCKER_FAKE_UP_ALWAYS is set)
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
  { printf -- '--- %s\n' "$*"; env; } >> "` + logPath + `.env"
  for a in "$@"; do
    if [ "$a" = "config" ]; then
      exec "` + filepath.ToSlash(real) + `" "$@"
    fi
  done
  case " $* " in
    *" exec "*|*" run "*) printf '%s' "$DOCKER_FAKE_EXEC_OUT"; exit "${DOCKER_FAKE_EXEC_EXIT:-0}" ;;
    *" up "*)
      if [ -n "$DOCKER_FAKE_UP_MSG" ] && { [ -n "$DOCKER_FAKE_UP_ALWAYS" ] || [ ! -e "` + logPath + `.upfail" ]; }; then
        : > "` + logPath + `.upfail"
        printf '%s\n' "$DOCKER_FAKE_UP_MSG" >&2
        exit 1
      fi ;;
    *" ps "*) printf '%s' "$DOCKER_FAKE_STATUS" ;;
    *" logs "*) printf '%s' "$DOCKER_FAKE_LOGS" ;;
  esac
  exit "${DOCKER_FAKE_EXIT:-0}"
fi
if [ "$1" = "network" ] && [ "$2" = "ls" ]; then
  printf '%s' "$DOCKER_FAKE_NETWORKS"
  exit 0
fi
if [ "$1" = "network" ] && [ "$2" = "inspect" ]; then
  printf '%s' "$DOCKER_FAKE_NET_ENDPOINTS"
  exit 0
fi
if [ "$1" = "container" ] && [ "$2" = "inspect" ]; then
  for c in $DOCKER_FAKE_CONTAINERS; do
    if [ "$c" = "$5" ]; then printf true; exit 0; fi
  done
  exit 1
fi
if [ "$1" = "volume" ] && [ "$2" = "ls" ]; then
  printf '%s' "$DOCKER_FAKE_VOLUMES"
  exit 0
fi
if [ "$1" = "volume" ] && [ "$2" = "inspect" ]; then
  exit "${DOCKER_FAKE_VOLUME_EXIT:-0}"
fi
if [ "$1" = "context" ] && [ "$2" = "inspect" ]; then
  printf '%s' '[{"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock"}}}]'
  exit 0
fi
if [ "$1" = "ps" ]; then
  case " $* " in
    *" -a "*) printf '%s' "${DOCKER_FAKE_PS_ALL:-$DOCKER_FAKE_PS}" ;;
    *"` + identity.LegacyLabelRoot + `"*) printf '%s' "$DOCKER_FAKE_PS_LEGACY" ;;
    *) printf '%s' "$DOCKER_FAKE_PS" ;;
  esac
  exit 0
fi
if [ "$1" = "inspect" ]; then
  case " $* " in
    *".State.Running"*) printf '%s' "${DOCKER_FAKE_PROXY_RUNNING:-false}" ;;
    *"HostConfig.NetworkMode"*) [ -n "$DOCKER_FAKE_PROXY_MISSING" ] && exit 1; printf '%s' "${DOCKER_FAKE_PROXY_NETMODE:-bridge}" ;;
    *) printf '%s' "$DOCKER_FAKE_PROXY_PORT" ;;
  esac
  exit 0
fi
exit 0
`
	testsupport.FakeExe(t, filepath.Join(dir, "docker"), script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// requireDocker skips a test that renders a real Compose config
// (`docker compose config`) on a machine without Docker, such as a GitHub
// macOS runner.
func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
}

// dockerEnvLog is what installFakeDocker's `docker compose` calls saw in
// their environment ("--- args" header, then KEY=value lines, per call).
func dockerEnvLog(logPath string) string {
	b, _ := os.ReadFile(logPath + ".env")
	return string(b)
}
