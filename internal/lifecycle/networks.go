package lifecycle

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
)

// Docker keeps a network endpoint after a crash or a full disk even though
// its container is gone; the next `up` then fails with "endpoint with name X
// already exists in network N". Everything below touches only networks that
// carry this project's compose label, never another project's.

var staleEndpointMsg = regexp.MustCompile(`endpoint with name (\S+) already exists in network (\S+)`)

func docker(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	cmd.Env = compose.DockerEnv(nil)
	out, err := cmd.Output()
	return string(out), err
}

// projectNetworks lists the networks labelled as belonging to project.
func projectNetworks(project string) []string {
	out, err := docker("network", "ls", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Name}}")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// containerState reports whether a container of that name exists and runs.
func containerState(name string) (exists, running bool) {
	out, err := docker("container", "inspect", "-f", "{{.State.Running}}", name)
	return err == nil, strings.TrimSpace(out) == "true"
}

// CleanProjectNetworks force-disconnects, from each of project's networks,
// the endpoints whose container no longer exists, then removes the network
// (Docker refuses while a live endpoint, such as the proxy, remains: fine).
func CleanProjectNetworks(stderr io.Writer, project string) {
	for _, n := range projectNetworks(project) {
		out, err := docker("network", "inspect", n, "--format", `{{range .Containers}}{{.Name}}{{"\n"}}{{end}}`)
		if err != nil {
			continue
		}
		for _, ep := range strings.Fields(out) {
			if exists, _ := containerState(ep); !exists {
				if _, err := docker("network", "disconnect", "-f", n, ep); err == nil {
					fmt.Fprintf(stderr, "stackfork: removed stale endpoint %s from network %s\n", ep, n)
				}
			}
		}
		docker("network", "rm", n) //nolint:errcheck // still in use by another container: leave it
	}
}

// RecoverStaleEndpoint reads a failed `up`'s stderr and, when it names a stale
// endpoint on one of project's networks whose container does not run,
// force-disconnects it. It reports whether the caller should retry.
func RecoverStaleEndpoint(stderr io.Writer, project, failure string) bool {
	m := staleEndpointMsg.FindStringSubmatch(failure)
	if m == nil {
		return false
	}
	name, network := m[1], m[2]
	own := false
	for _, n := range projectNetworks(project) {
		own = own || n == network
	}
	if _, running := containerState(name); !own || running {
		return false
	}
	if _, err := docker("network", "disconnect", "-f", network, name); err != nil {
		return false
	}
	fmt.Fprintf(stderr, "stackfork: removed stale endpoint %s from network %s; trying up again\n", name, network)
	return true
}
