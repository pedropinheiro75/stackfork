// proxy.go wires internal/lifecycle's proxy functions into the CLI: `proxy
// up|down|status|reload` (task 9.1/9.2) and `route add|remove|list` for
// host-run apps (task 9.3). The route table generation and the proxy
// up/down/reload calls themselves live in internal/lifecycle (task 4.1) so
// the dashboard's proxy-up/down/reload actions run through the exact same
// code.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
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
		info, err := lifecycle.ProxyUp(newStateBase, legacyStateBase, port)
		if err != nil {
			return 0, err
		}
		return 0, printJSON(stdout, info)
	case "down":
		info, err := lifecycle.ProxyDown()
		if err != nil {
			return 0, err
		}
		return 0, printJSON(stdout, info)
	case "status":
		info, err := proxy.Status(newStateBase)
		if err != nil {
			return 0, err
		}
		return 0, printJSON(stdout, info)
	case "reload":
		if err := lifecycle.ProxyReload(newStateBase, legacyStateBase); err != nil {
			return 0, err
		}
		return 0, nil
	default:
		return 2, fmt.Errorf("unknown proxy subcommand %q", sub)
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
		info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "route")
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
		info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "route")
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
		info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "route")
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

	lifecycle.ReloadProxyIfRunning(stderr, newStateBase, legacyStateBase)
	return nil
}
