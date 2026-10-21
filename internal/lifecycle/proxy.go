package lifecycle

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// ProxyUp starts the shared proxy on port, generating its Caddyfile from
// every manifest first (design.md D7 layer 3). The one function `proxy up`
// and the dashboard's proxy-up action both call (design.md D4).
func ProxyUp(newStateBase, legacyStateBase string, port int) (proxy.Info, error) {
	if err := LocalDocker(); err != nil {
		return proxy.Info{}, err
	}
	mode, err := proxy.Mode()
	if err != nil {
		return proxy.Info{}, err
	}
	err = proxy.WithLock(newStateBase, func() error {
		routes, err := GatherRoutes(newStateBase, legacyStateBase, mode)
		if err != nil {
			return err
		}
		if err := proxy.WriteCaddyfile(newStateBase, routes, mode, port); err != nil {
			return err
		}
		return proxy.Up(newStateBase, port, mode)
	})
	if err != nil {
		return proxy.Info{}, err
	}
	return proxy.Info{Name: proxy.ContainerName(), Running: true, Port: port}, nil
}

// ProxyDown stops the shared proxy.
func ProxyDown() (proxy.Info, error) {
	if err := LocalDocker(); err != nil {
		return proxy.Info{}, err
	}
	if err := proxy.Down(); err != nil {
		return proxy.Info{}, err
	}
	return proxy.Info{Name: proxy.ContainerName(), Running: false, Port: 0}, nil
}

// ProxyReload regenerates the Caddyfile from every manifest and asks the
// running proxy to pick it up, keeping whatever port is already configured
// (proxy.ReadPort) and network Mode the daemon needs.
func ProxyReload(newStateBase, legacyStateBase string) error {
	if err := LocalDocker(); err != nil {
		return err
	}
	mode, err := proxy.Mode()
	if err != nil {
		return err
	}
	port, err := proxy.ReadPort(newStateBase)
	if err != nil {
		return err
	}
	return proxy.WithLock(newStateBase, func() error {
		routes, err := GatherRoutes(newStateBase, legacyStateBase, mode)
		if err != nil {
			return err
		}
		if err := proxy.WriteCaddyfile(newStateBase, routes, mode, port); err != nil {
			return err
		}
		return proxy.Reload(newStateBase)
	})
}

// ReloadProxyIfRunning is called after `up`/`down`/`route` change which
// instances are routable (task 9.2). Best effort: never fails the caller,
// only warns on stderr.
func ReloadProxyIfRunning(stderr io.Writer, newStateBase, legacyStateBase string) {
	running, err := proxy.IsRunning(proxy.ContainerName())
	if err != nil || !running {
		return
	}
	if err := ProxyReload(newStateBase, legacyStateBase); err != nil {
		fmt.Fprintf(stderr, "warning: proxy reload failed: %s\n", err)
	}
}

// GatherRoutes builds the proxy's full route table from every manifest in
// both state directories (task 9.2: "all RUNNING instances' manifests (both
// state dirs)"). A manifest that fails to resolve to a route (invalid
// hostname, instance not running, no primary HTTP service, ...) is skipped
// rather than failing the whole generation - one broken instance must never
// take every other instance's routing down.
func GatherRoutes(newStateBase, legacyStateBase, mode string) ([]proxy.Route, error) {
	var routes []proxy.Route
	for _, dirPrefix := range []struct{ base, prefix string }{
		{newStateBase, identity.Prefix},
		{legacyStateBase, identity.LegacyPrefix},
	} {
		matches, err := state.Manifests(dirPrefix.base, dirPrefix.prefix)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			info, err := state.ReadManifest(m)
			if err != nil || info == nil {
				continue
			}
			if route, ok := instanceRoute(info, filepath.Dir(m), mode); ok {
				routes = append(routes, route)
			}
		}
	}
	return routes, nil
}

// instanceRoute resolves one instance's manifest to a proxy Route through
// knowledge.ResolveEntry: a registered host-run route wins when present
// (task 9.3), else the recorded entry, else the primary HTTP service, on its
// currently published port, re-resolved via `docker compose ps` every time
// (task 9.2) so a random-port/legacy instance's route never goes stale.
// `none` and a recorded entry the stack lacks yield no route. Only a RUNNING instance yields a route. mode picks the
// target's address (proxy.HostTarget, design.md D7).
func instanceRoute(info *state.Manifest, instanceDir, mode string) (proxy.Route, bool) {
	hostname, err := identity.BrowserHostname(info.Project, info.Hostname)
	if err != nil {
		return proxy.Route{}, false
	}

	rows, running := instancePsRows(info.Project, info.Root, instanceDir)
	if !running {
		return proxy.Route{}, false
	}

	configPath := filepath.Join(instanceDir, "compose.json")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return proxy.Route{}, false
	}
	model, err := compose.DecodeJSON(configData)
	if err != nil {
		return proxy.Route{}, false
	}
	// A knowledge file that does not load counts as no entry (D6).
	k, _ := knowledge.LoadForCheckout(info.Root)
	entry := knowledge.ResolveEntry(k, model, info.Routes)
	if !entry.OK {
		return proxy.Route{}, false
	}
	if entry.Source == knowledge.SourceHostRoute {
		return proxy.Route{Hostname: hostname, Target: proxy.HostTarget(mode, entry.Port)}, true
	}
	hostPort, ok := compose.PublishedHostPort(rows, entry.Service, entry.Port)
	if !ok {
		return proxy.Route{}, false
	}
	return proxy.Route{Hostname: hostname, Target: proxy.HostTarget(mode, hostPort)}, true
}

// instancePsRows runs `docker compose ps` for one instance's snapshot and
// reports whether any container in it is currently running. A missing
// snapshot or a docker error is reported as "not running" (skip, don't fail
// generation) since a gc-eligible or half-deleted instance must not break
// every other route.
func instancePsRows(project, root, instanceDir string) ([]compose.StatusRow, bool) {
	configPath := filepath.Join(instanceDir, "compose.json")
	if _, err := os.Stat(configPath); err != nil {
		return nil, false
	}
	cmd := exec.Command("docker", "compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath, "--profile", "*", "ps", "--all", "--format", "json")
	cmd.Env = compose.DockerEnv(nil)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	rows, err := compose.PublicStatus(out.String())
	if err != nil {
		return nil, false
	}
	for _, row := range rows {
		if strings.EqualFold(row.State, "running") {
			return rows, true
		}
	}
	return rows, false
}
