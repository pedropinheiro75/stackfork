// doctor.go implements `stackfork doctor` (task 9.6, design.md D7 layer 3):
// checks the local environment the proxy and the rest of the tool depend on,
// printing each with a fix hint. Exits non-zero only when a check fails
// outright (an unavailable optional piece, such as Docker itself, warns
// instead).
package cli

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
)

type doctorCheck struct {
	name, status, message, hint string
}

func cmdDoctor(stdout io.Writer, subArgs []string, newStateBase string) (int, error) {
	var checks []doctorCheck
	add := func(name string, status, message, hint string) {
		checks = append(checks, doctorCheck{name, status, message, hint})
	}

	{
		status, message, hint := checkDockerContext()
		add("docker context", status, message, hint)
	}
	{
		status, message, hint := checkComposeVersion()
		add("compose --no-env-resolution", status, message, hint)
	}
	{
		status, message, hint := checkLocalhostResolution()
		add("*.localhost resolution", status, message, hint)
	}
	{
		status, message, hint := checkProxyPort(newStateBase)
		add("proxy port", status, message, hint)
	}
	if dockerAvailable() {
		status, message, hint := checkHostDockerInternal()
		add("host.docker.internal", status, message, hint)
	} else {
		add("host.docker.internal", "warn", "Docker daemon not reachable; skipped",
			"start Docker (or select a reachable context) to run this check")
	}

	failed := false
	for _, c := range checks {
		line := fmt.Sprintf("[%s] %s: %s", c.status, c.name, c.message)
		if c.hint != "" {
			line += " (fix: " + c.hint + ")"
		}
		fmt.Fprintln(stdout, line)
		if c.status == "fail" {
			failed = true
		}
	}
	if failed {
		return 1, nil
	}
	return 0, nil
}

func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "info").Run() == nil
}

func checkDockerContext() (status, message, hint string) {
	if err := lifecycle.LocalDocker(); err != nil {
		return "fail", err.Error(), "select a local Docker context, e.g. `docker context use default`"
	}
	return "ok", "a local Docker context is selected", ""
}

// checkComposeVersion checks the flag exists (task 9.6: "Compose version
// supports config --no-env-resolution") by asking Compose's own --help
// rather than parsing a version number against a hand-maintained threshold.
func checkComposeVersion() (status, message, hint string) {
	out, err := exec.Command("docker", "compose", "config", "--help").CombinedOutput()
	if err != nil {
		return "warn", "could not run `docker compose config --help`", "install Docker Compose v2"
	}
	if !strings.Contains(string(out), "no-env-resolution") {
		return "fail", "installed Compose does not support --no-env-resolution", "upgrade Docker Compose to v2.24 or newer"
	}
	return "ok", "docker compose config supports --no-env-resolution", ""
}

// checkLocalhostResolution probes a random label (task 9.6: "net.LookupHost
// on a random x-<rand>.localhost") so a stale DNS cache entry for a specific
// name already used before can't mask the check.
func checkLocalhostResolution() (status, message, hint string) {
	name := fmt.Sprintf("x-%d.localhost", rand.Int63())
	ips, err := net.LookupHost(name)
	if err != nil {
		return "fail", fmt.Sprintf("%s did not resolve: %s", name, err),
			"add `127.0.0.1 <hostname>.localhost` lines to /etc/hosts, or configure a resolver for *.localhost"
	}
	for _, ip := range ips {
		if ip == "127.0.0.1" || ip == "::1" {
			return "ok", fmt.Sprintf("%s resolves to loopback", name), ""
		}
	}
	return "fail", fmt.Sprintf("%s resolved to %v, not loopback", name, ips),
		"check /etc/hosts and your resolver config for a stray *.localhost override"
}

// checkProxyPort never binds the port itself (task 9.6/safety: this machine
// runs real stacks, possibly already on 80) - it only dials it, and only
// flags a problem when something other than stackfork-proxy answers.
func checkProxyPort(newStateBase string) (status, message, hint string) {
	port, err := proxy.ReadPort(newStateBase)
	if err != nil {
		return "warn", fmt.Sprintf("could not read the configured proxy port: %s", err), ""
	}
	conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if dialErr != nil {
		return "ok", fmt.Sprintf("port %d is free", port), ""
	}
	conn.Close()

	name := proxy.ContainerName()
	running, _ := proxy.IsRunning(name)
	if running {
		return "ok", fmt.Sprintf("port %d is held by %s", port, name), ""
	}
	return "warn", fmt.Sprintf("port %d is already in use by something other than %s", port, name),
		fmt.Sprintf("free port %d, or run `stackfork proxy up --port N` with a different one", port)
}

// checkHostDockerInternal runs a throwaway, --rm alpine container (task
// 9.6/safety: "only against a throwaway alpine container that is removed")
// with a short timeout, mirroring the --add-host the real proxy container
// gets on Linux.
func checkHostDockerInternal() (status, message, hint string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm",
		"--add-host", "host.docker.internal:host-gateway",
		"alpine:3.20", "ping", "-c", "1", "-W", "2", "host.docker.internal")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "fail", fmt.Sprintf("host.docker.internal unreachable from a container: %s", strings.TrimSpace(string(out))),
			"on Linux this needs Docker >=20.10 for --add-host host-gateway; on macOS/Windows check Docker Desktop/OrbStack is running"
	}
	return "ok", "host.docker.internal reachable from a container", ""
}
