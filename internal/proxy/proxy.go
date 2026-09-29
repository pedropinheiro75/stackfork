// Package proxy runs the tool-owned Caddy container that gives instances
// portless hostname URLs (task 9.1, design.md D7 layer 3): one container,
// no Docker socket, loopback-only exposure. Two network modes depending on
// the Docker daemon (see Mode): "bridge" (Docker Desktop, OrbStack and
// similar VM-based engines) routes to host.docker.internal, which those
// engines proxy to the host's own loopback themselves; "host" (a genuine
// native Linux dockerd, where host-gateway is just a bridge IP that can't
// reach a 127.0.0.1-bound port) runs the proxy with --network host and
// routes directly to 127.0.0.1.
package proxy

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const (
	// Image is pinned (task 9.1: "official caddy:2-alpine image, pinned
	// tag") so the proxy's behaviour doesn't shift under a floating tag.
	Image = "caddy:2-alpine"
	// DefaultPort is used when no port was ever configured.
	DefaultPort = 80
)

// ContainerName is the proxy container's name: overridable via
// STACKFORK_PROXY_NAME so tests never touch a real stackfork-proxy.
func ContainerName() string {
	if v := os.Getenv("STACKFORK_PROXY_NAME"); v != "" {
		return v
	}
	return "stackfork-proxy"
}

// Dir is the proxy's own directory under the tool's (non-legacy) state base:
// <state>/proxy/ (task 9.1).
func Dir(stateBase string) string { return filepath.Join(stateBase, "proxy") }

// CaddyfilePath is the generated config, bind-mounted read-only into the
// container (task 9.1).
func CaddyfilePath(stateBase string) string { return filepath.Join(Dir(stateBase), "Caddyfile") }

func portFilePath(stateBase string) string { return filepath.Join(Dir(stateBase), "port") }

// ReadPort returns the persisted proxy port, or DefaultPort if none was ever
// configured (task 9.1: "the port must be configurable and stored").
func ReadPort(stateBase string) (int, error) {
	data, err := os.ReadFile(portFilePath(stateBase))
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultPort, nil
		}
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("corrupt proxy port file: %w", err)
	}
	return n, nil
}

// WritePort persists the configured proxy port (0600, the state-files
// invariant).
func WritePort(stateBase string, port int) error {
	if err := os.MkdirAll(Dir(stateBase), 0o700); err != nil {
		return err
	}
	return os.WriteFile(portFilePath(stateBase), []byte(strconv.Itoa(port)+"\n"), 0o600)
}

// Route is one Caddy site: hostname -> the target that serves it (via
// HostTarget, mode-dependent), for a container-published (stable or
// currently-resolved random) port or a host-run app registered via `route
// add` alike (task 9.2/9.3).
type Route struct {
	Hostname string
	Target   string
}

// Mode reports which network mode the proxy container must run in
// (design.md D7), from the Docker daemon's own OS/runtime - never
// runtime.GOOS, since the daemon a `docker` client talks to is not
// necessarily on the same machine/OS as the process invoking it
// (DOCKER_HOST, a remote context, stackfork itself running inside a
// container, ...), and a Linux VM behind Docker Desktop/OrbStack (OSType is
// always "linux" there too) behaves like macOS, not like a native Linux
// host.
//
//   - "bridge" (the original, still the default everywhere but native
//     Linux): the proxy joins no network but its own bridge, publishes
//     127.0.0.1:<port>->80, and reaches every target through
//     host.docker.internal:host-gateway. Correct for Docker Desktop,
//     OrbStack and similar VM-based engines, which proxy that special
//     hostname to the host's own loopback themselves.
//   - "host": a genuine native Linux dockerd's host-gateway only ever
//     resolves to the bridge network's own gateway IP, which cannot reach a
//     service published on 127.0.0.1 - reproduced live on Linux CI
//     (connection reset / never reachable). The proxy instead runs with
//     --network host (sharing the host's network namespace directly, so it
//     can reach 127.0.0.1 itself) and Caddy binds only 127.0.0.1:<port>
//     (never 0.0.0.0, verified directly: "bind 127.0.0.1" keeps the same
//     loopback-only exposure bridge mode's own docker -p 127.0.0.1:...
//     always had).
//
// Detection is a documented heuristic (ponytail: the known ceiling is any
// VM-based engine not named in vmEngines below; the upgrade path is adding
// its OperatingSystem marker there, not a network probe).
func Mode() (string, error) {
	out, err := exec.Command("docker", "info", "--format", "{{.OSType}}|{{.OperatingSystem}}").Output()
	if err != nil {
		return "", fmt.Errorf("docker info: %w", err)
	}
	osType, operatingSystem, _ := strings.Cut(strings.TrimSpace(string(out)), "|")
	return modeFromDaemonInfo(osType, operatingSystem), nil
}

// modeFromDaemonInfo is Mode's heuristic, split out so it's unit-testable
// without a real Docker daemon.
func modeFromDaemonInfo(osType, operatingSystem string) string {
	if osType != "linux" {
		return "bridge"
	}
	lower := strings.ToLower(operatingSystem)
	for _, engine := range vmEngines {
		if strings.Contains(lower, engine) {
			return "bridge"
		}
	}
	return "host"
}

// vmEngines names every known VM-based Docker engine whose Linux guest
// still needs bridge mode - see Mode's doc comment for the ceiling this
// list represents.
var vmEngines = []string{"docker desktop", "orbstack", "rancher desktop", "colima", "lima"}

// HostTarget is the address a Caddy route reaches a host-published port
// through, for the network Mode a proxy cycle is using.
func HostTarget(mode string, port int) string {
	if mode == "host" {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	return fmt.Sprintf("host.docker.internal:%d", port)
}

// GenerateCaddyfile renders the config from every routed instance (task
// 9.2): `auto_https off` in the global block, since Caddy must never try to
// get a TLS cert for a .localhost name, and one site per route, sorted by
// hostname for a deterministic, diff-friendly file. In "host" mode every
// site must explicitly bind 127.0.0.1 and declare the proxy's own port
// (the container shares the host's network namespace directly, so nothing
// else restricts which interface it listens on); in "bridge" mode a plain
// `http://<hostname>` address is enough, since docker's own -p
// 127.0.0.1:<port>:80 already restricts exposure.
func GenerateCaddyfile(routes []Route, mode string, port int) string {
	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Hostname < sorted[j].Hostname })

	var b strings.Builder
	b.WriteString("{\n\tauto_https off\n}\n")
	for _, r := range sorted {
		if mode == "host" {
			fmt.Fprintf(&b, "\nhttp://%s:%d {\n\tbind 127.0.0.1\n\treverse_proxy %s\n}\n", r.Hostname, port, r.Target)
		} else {
			fmt.Fprintf(&b, "\nhttp://%s {\n\treverse_proxy %s\n}\n", r.Hostname, r.Target)
		}
	}
	return b.String()
}

// WriteCaddyfile generates and writes the config (0600, private-writes
// invariant). Deliberately NOT temp-file+rename, unlike state.WritePrivate:
// Up() bind-mounts this exact path into the proxy container as a single
// file (`-v CaddyfilePath:/etc/caddy/Caddyfile:ro`), and Docker's single-file
// bind mount is tied to the inode present at mount time, not the path -
// verified directly against this machine's Docker: renaming a replacement
// over a bind-mounted single file leaves the container seeing "No such file
// or directory" from then on, breaking every route until the proxy is
// recreated. A torn write is not a real hazard here to trade that safety
// away for: Caddy never re-reads the file on its own, only when WithLock's
// caller explicitly runs `caddy reload` immediately after this call returns,
// so the write is always complete before anything reads it - WithLock's
// flock is what actually needs to serialize callers, not this function.
func WriteCaddyfile(stateBase string, routes []Route, mode string, port int) error {
	if err := os.MkdirAll(Dir(stateBase), 0o700); err != nil {
		return err
	}
	data := []byte(GenerateCaddyfile(routes, mode, port))
	return os.WriteFile(CaddyfilePath(stateBase), data, 0o600)
}

// WithLock serializes a full "gather routes -> write Caddyfile -> reload"
// sequence under an flock on a .lock file in the proxy's own state dir
// (design.md D7 layer 3): without it, two callers racing (e.g. two `up`s
// finishing around the same time, or a CLI reload racing the dashboard's own
// register/unregister) can each read a route table, then write theirs back
// in an order that drops the other's route - a lost update. Every caller
// that does the full sequence must go through this, not just call
// WriteCaddyfile directly.
func WithLock(stateBase string, fn func() error) error {
	dir := Dir(stateBase)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := state.AcquireLock(filepath.Join(dir, ".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	return fn()
}

// IsRunning reports whether the proxy container exists and is running. A
// docker error (not found, daemon unreachable, ...) is treated as "not
// running" so every caller's "if the proxy isn't running, skip silently"
// rule (task 9.2) falls out naturally.
func IsRunning(name string) (bool, error) {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// Up starts the proxy container on port in the given network Mode,
// replacing any existing one bound to a different port; recreates a
// stopped container left behind by a crash. The Caddyfile at
// CaddyfilePath(stateBase) must already exist - callers write it with
// WriteCaddyfile first (with this same mode/port), since Docker would
// otherwise bind-mount an empty directory in its place. No Docker socket is
// mounted (task 9.1) and exposure stays loopback-only either way: "bridge"
// mode joins no other network and publishes 127.0.0.1:<port>->80; "host"
// mode shares the host's network namespace directly (no docker -p at all)
// but Caddy itself only ever binds 127.0.0.1 (GenerateCaddyfile).
func Up(stateBase string, port int, mode string) error {
	name := ContainerName()
	running, err := IsRunning(name)
	if err != nil {
		return err
	}
	if running {
		current, ok := currentPort(stateBase, name, mode)
		if ok && current == port {
			return Reload(stateBase)
		}
	}

	// Replace whatever's there (stopped, or running on a different port).
	exec.Command("docker", "rm", "-f", name).Run() //nolint:errcheck // best effort; docker run below reports the real failure

	if err := WritePort(stateBase, port); err != nil {
		return err
	}
	args := []string{"run", "-d", "--name", name, "--label", identity.LabelProxy + "=true"}
	if mode == "host" {
		args = append(args, "--network", "host")
	} else {
		args = append(args,
			"-p", fmt.Sprintf("127.0.0.1:%d:80", port),
			"--add-host", "host.docker.internal:host-gateway",
		)
	}
	args = append(args, "-v", CaddyfilePath(stateBase)+":/etc/caddy/Caddyfile:ro", Image)
	cmd := exec.Command("docker", args...)
	cmd.Env = compose.DockerEnv()
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker run %s: %s", name, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// currentPort reports the port the running proxy container is actually
// serving on, so Up can tell "already running on the requested port" (a
// no-op reload) from "running on a stale port" (must be recreated). Bridge
// mode reads it off docker's own port mapping; host mode has none to read
// (no docker -p at all, --network host shares the namespace directly), so
// it trusts the port file Up itself always writes right before starting
// the container - which is exactly what the running container was told to
// bind (GenerateCaddyfile bakes the port into every site address).
func currentPort(stateBase, name, mode string) (int, bool) {
	if mode == "host" {
		p, err := ReadPort(stateBase)
		if err != nil {
			return 0, false
		}
		return p, true
	}
	out, err := exec.Command("docker", "inspect", "-f",
		`{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostPort}}`, name).Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// Down stops and removes the proxy container, if any. The instance state
// under <state>/proxy/ (Caddyfile, port) is left alone, mirroring every
// other `down`: runtime stops, state persists.
func Down() error {
	name := ContainerName()
	out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("docker rm -f %s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

// Reload regenerates nothing itself (the caller must WriteCaddyfile first);
// it only asks the running Caddy process to pick up the file it already has
// mounted (task 9.2). Skips silently if the proxy isn't running.
//
// A reload requested right after the container starts (a caller registering
// a route in the same breath as `proxy up`, e.g. the dashboard) can race
// Caddy's own admin API startup inside the container - `docker exec` runs
// fine, but `caddy reload` gets "connection refused" for a few hundred
// milliseconds. Retried briefly rather than surfaced as a failure, since
// the container is confirmed running and the file is already in place; a
// reload that still fails after that really is one.
func Reload(stateBase string) error {
	name := ContainerName()
	running, err := IsRunning(name)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	var out []byte
	for attempt := 0; attempt < 10; attempt++ {
		out, err = exec.Command("docker", "exec", name, "caddy", "reload", "--config", "/etc/caddy/Caddyfile").CombinedOutput()
		if err == nil {
			return nil
		}
		if !strings.Contains(string(out), "connection refused") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("caddy reload: %s", strings.TrimSpace(string(out)))
}

// Info is `proxy status`'s output.
type Info struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	Port    int    `json:"port"`
}

// Status reports the proxy's current name/running/port, for `proxy status`
// and for `urls`/`doctor` to check without duplicating the docker inspect
// call.
func Status(stateBase string) (Info, error) {
	name := ContainerName()
	running, err := IsRunning(name)
	if err != nil {
		return Info{}, err
	}
	port, err := ReadPort(stateBase)
	if err != nil {
		return Info{}, err
	}
	return Info{Name: name, Running: running, Port: port}, nil
}
