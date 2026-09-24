// Package proxy runs the tool-owned Caddy container that gives instances
// portless hostname URLs (task 9.1, design.md D7 layer 3): one container,
// no Docker socket, no network joins, routing to host.docker.internal on
// every platform (host-gateway on Linux, built in on Docker
// Desktop/OrbStack).
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

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
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

// Route is one Caddy site: hostname -> the host.docker.internal target that
// serves it, for a container-published (stable or currently-resolved
// random) port or a host-run app registered via `route add` alike (task
// 9.2/9.3).
type Route struct {
	Hostname string
	Target   string
}

// GenerateCaddyfile renders the config from every routed instance (task
// 9.2): `auto_https off` in the global block, since Caddy must never try to
// get a TLS cert for a .localhost name, and one `http://<hostname>` site per
// route. Sorted by hostname for a deterministic, diff-friendly file.
func GenerateCaddyfile(routes []Route) string {
	sorted := append([]Route(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Hostname < sorted[j].Hostname })

	var b strings.Builder
	b.WriteString("{\n\tauto_https off\n}\n")
	for _, r := range sorted {
		fmt.Fprintf(&b, "\nhttp://%s {\n\treverse_proxy %s\n}\n", r.Hostname, r.Target)
	}
	return b.String()
}

// WriteCaddyfile generates and writes the config (0600, private-writes
// invariant).
func WriteCaddyfile(stateBase string, routes []Route) error {
	if err := os.MkdirAll(Dir(stateBase), 0o700); err != nil {
		return err
	}
	data := []byte(GenerateCaddyfile(routes))
	return os.WriteFile(CaddyfilePath(stateBase), data, 0o600)
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

// Up starts the proxy container on port, replacing any existing one bound to
// a different port; recreates a stopped container left behind by a crash.
// The Caddyfile at CaddyfilePath(stateBase) must already exist - callers
// write it with WriteCaddyfile first, since Docker would otherwise bind-mount
// an empty directory in its place. No Docker socket is mounted and no
// networks are joined (task 9.1): the container only publishes 127.0.0.1 and
// reaches every target through host.docker.internal.
func Up(stateBase string, port int) error {
	name := ContainerName()
	running, err := IsRunning(name)
	if err != nil {
		return err
	}
	if running {
		current, ok := publishedPort(name)
		if ok && current == port {
			return Reload(stateBase)
		}
	}

	// Replace whatever's there (stopped, or running on a different port).
	exec.Command("docker", "rm", "-f", name).Run() //nolint:errcheck // best effort; docker run below reports the real failure

	if err := WritePort(stateBase, port); err != nil {
		return err
	}
	args := []string{
		"run", "-d", "--name", name,
		"--label", identity.LabelProxy + "=true",
		"-p", fmt.Sprintf("127.0.0.1:%d:80", port),
		"--add-host", "host.docker.internal:host-gateway",
		"-v", CaddyfilePath(stateBase) + ":/etc/caddy/Caddyfile:ro",
		Image,
	}
	cmd := exec.Command("docker", args...)
	cmd.Env = compose.DockerEnv()
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker run %s: %s", name, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// publishedPort reads the host port currently bound to the container's
// published 80/tcp, so Up can tell "already running on the requested port"
// (a no-op reload) from "running on a stale port" (must be recreated).
func publishedPort(name string) (int, bool) {
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
func Reload(stateBase string) error {
	name := ContainerName()
	running, err := IsRunning(name)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	out, err := exec.Command("docker", "exec", name, "caddy", "reload", "--config", "/etc/caddy/Caddyfile").CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy reload: %s", strings.TrimSpace(string(out)))
	}
	return nil
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
