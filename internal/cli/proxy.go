// proxy.go wires the internal/proxy package into the CLI: `proxy
// up|down|status|reload` (task 9.1/9.2) and `route add|remove|list` for
// host-run apps (task 9.3), plus the Caddyfile generation that walks every
// manifest in both state directories.
package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// --- proxy up/down/status/reload -------------------------------------------

func cmdProxy(stdout, stderr io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	if len(subArgs) == 0 {
		return 2, errors.New("proxy requires a subcommand (up, down, status, reload)")
	}
	sub := subArgs[0]
	rest := subArgs[1:]

	switch sub {
	case "up":
		fs := flag.NewFlagSet("proxy up", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		portFlag := fs.Int("port", 0, "")
		if err := fs.Parse(rest); err != nil {
			return 2, fmt.Errorf("%s", err.Error())
		}
		port := *portFlag
		if port == 0 {
			p, err := proxy.ReadPort(newStateBase)
			if err != nil {
				return 0, err
			}
			port = p
		}
		if err := localDocker(); err != nil {
			return 0, err
		}
		routes, err := gatherRoutes(newStateBase, legacyStateBase)
		if err != nil {
			return 0, err
		}
		if err := proxy.WriteCaddyfile(newStateBase, routes); err != nil {
			return 0, err
		}
		if err := proxy.Up(newStateBase, port); err != nil {
			return 0, err
		}
		return 0, printJSON(stdout, proxy.Info{Name: proxy.ContainerName(), Running: true, Port: port})
	case "down":
		if err := localDocker(); err != nil {
			return 0, err
		}
		if err := proxy.Down(); err != nil {
			return 0, err
		}
		return 0, printJSON(stdout, proxy.Info{Name: proxy.ContainerName(), Running: false, Port: 0})
	case "status":
		info, err := proxy.Status(newStateBase)
		if err != nil {
			return 0, err
		}
		return 0, printJSON(stdout, info)
	case "reload":
		if err := localDocker(); err != nil {
			return 0, err
		}
		routes, err := gatherRoutes(newStateBase, legacyStateBase)
		if err != nil {
			return 0, err
		}
		if err := proxy.WriteCaddyfile(newStateBase, routes); err != nil {
			return 0, err
		}
		if err := proxy.Reload(newStateBase); err != nil {
			return 0, err
		}
		return 0, nil
	default:
		return 2, fmt.Errorf("unknown proxy subcommand %q", sub)
	}
}

// reloadProxyIfRunning is called after `up`/`down` change which instances
// are running (task 9.2). Best effort: never fails the caller's up/down,
// only warns on stderr.
func reloadProxyIfRunning(stderr io.Writer, newStateBase, legacyStateBase string) {
	running, err := proxy.IsRunning(proxy.ContainerName())
	if err != nil || !running {
		return
	}
	routes, err := gatherRoutes(newStateBase, legacyStateBase)
	if err != nil {
		fmt.Fprintf(stderr, "warning: proxy reload skipped: %s\n", err)
		return
	}
	if err := proxy.WriteCaddyfile(newStateBase, routes); err != nil {
		fmt.Fprintf(stderr, "warning: proxy reload skipped: %s\n", err)
		return
	}
	if err := proxy.Reload(newStateBase); err != nil {
		fmt.Fprintf(stderr, "warning: proxy reload failed: %s\n", err)
	}
}

// --- route add/remove/list --------------------------------------------------

func cmdRoute(stdout, stderr io.Writer, subArgs []string, root, branch, project, instanceDir, newStateBase, legacyStateBase string) error {
	if len(subArgs) == 0 {
		return errors.New("route requires a subcommand (add, remove, list)")
	}
	sub := subArgs[0]
	rest := subArgs[1:]
	manifestPath := filepath.Join(instanceDir, "manifest.json")

	switch sub {
	case "list":
		info, err := loadAndValidateManifest(instanceDir, root, branch, project, "route")
		if err != nil {
			return err
		}
		names := make([]string, 0, len(info.Routes))
		for name := range info.Routes {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			fmt.Fprintln(stdout, "No routes registered.")
			return nil
		}
		for _, name := range names {
			fmt.Fprintf(stdout, "%s: %d\n", name, info.Routes[name])
		}
		return nil
	case "add":
		if len(rest) != 2 {
			return errors.New("usage: route add <name> <port>")
		}
		port, err := strconv.Atoi(rest[1])
		if err != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("invalid port %q", rest[1])
		}
		info, err := loadAndValidateManifest(instanceDir, root, branch, project, "route")
		if err != nil {
			return err
		}
		if info.Routes == nil {
			info.Routes = map[string]int{}
		}
		info.Routes[rest[0]] = port
		if err := state.WritePrivate(manifestPath, info); err != nil {
			return err
		}
	case "remove":
		if len(rest) != 1 {
			return errors.New("usage: route remove <name>")
		}
		info, err := loadAndValidateManifest(instanceDir, root, branch, project, "route")
		if err != nil {
			return err
		}
		delete(info.Routes, rest[0])
		if err := state.WritePrivate(manifestPath, info); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown route subcommand %q", sub)
	}

	reloadProxyIfRunning(stderr, newStateBase, legacyStateBase)
	return nil
}

// --- Caddyfile generation ---------------------------------------------------

// gatherRoutes builds the proxy's full route table from every manifest in
// both state directories (task 9.2: "all RUNNING instances' manifests (both
// state dirs)"). A manifest that fails to resolve to a route (invalid
// hostname, instance not running, no primary HTTP service, ...) is skipped
// rather than failing the whole generation - one broken instance must never
// take every other instance's routing down.
func gatherRoutes(newStateBase, legacyStateBase string) ([]proxy.Route, error) {
	var routes []proxy.Route
	for _, dirPrefix := range []struct{ base, prefix string }{
		{newStateBase, identity.Prefix},
		{legacyStateBase, identity.LegacyPrefix},
	} {
		matches, err := filepath.Glob(filepath.Join(dirPrefix.base, dirPrefix.prefix+"-*", "manifest.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		for _, m := range matches {
			info, err := state.ReadManifest(m)
			if err != nil || info == nil {
				continue
			}
			if route, ok := instanceRoute(info, filepath.Dir(m)); ok {
				routes = append(routes, route)
			}
		}
	}
	return routes, nil
}

// instanceRoute resolves one instance's manifest to a proxy Route: a
// registered host-run route wins when present (task 9.3), else the primary
// HTTP service's currently published port, re-resolved via `docker compose
// ps` every time (task 9.2) so a random-port/legacy instance's route never
// goes stale. Only a RUNNING instance yields a route.
func instanceRoute(info *state.Manifest, instanceDir string) (proxy.Route, bool) {
	hostname, err := identity.BrowserHostname(info.Project, info.Hostname)
	if err != nil {
		return proxy.Route{}, false
	}

	rows, running := instancePsRows(info.Project, info.Root, instanceDir)
	if !running {
		return proxy.Route{}, false
	}

	if len(info.Routes) > 0 {
		names := make([]string, 0, len(info.Routes))
		for name := range info.Routes {
			names = append(names, name)
		}
		sort.Strings(names)
		port := info.Routes[names[0]]
		return proxy.Route{Hostname: hostname, Target: fmt.Sprintf("host.docker.internal:%d", port)}, true
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
	service, target, _, ok := compose.PrimaryHTTPService(model)
	if !ok {
		return proxy.Route{}, false
	}
	hostPort, ok := compose.PublishedHostPort(rows, service, target)
	if !ok {
		return proxy.Route{}, false
	}
	return proxy.Route{Hostname: hostname, Target: fmt.Sprintf("host.docker.internal:%d", hostPort)}, true
}

// instancePsRows runs `docker compose ps` for one instance's snapshot,
// mirroring cmdStatusURLs's own invocation, and reports whether any
// container in it is currently running. A missing snapshot or a docker
// error is reported as "not running" (skip, don't fail generation) since a
// gc-eligible or half-deleted instance must not break every other route.
func instancePsRows(project, root, instanceDir string) ([]compose.StatusRow, bool) {
	configPath := filepath.Join(instanceDir, "compose.json")
	if _, err := os.Stat(configPath); err != nil {
		return nil, false
	}
	cmd := exec.Command("docker", "compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath, "--profile", "*", "ps", "--all", "--format", "json")
	cmd.Env = compose.DockerEnv()
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
