package knowledge

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// HostCommands maps each recorded host app to the command it runs (the part
// of its supervisor command line after "--"), when known.
func HostCommands(info *state.Manifest) map[string][]string {
	out := map[string][]string{}
	for n, p := range info.HostProcs {
		args := p.Restart
		if len(args) == 0 {
			args = p.Cmdline
		}
		for i, a := range args {
			if a == "--" {
				out[n] = args[i+1:]
				break
			}
		}
	}
	return out
}

// ResolveEntryFor is ResolveEntry for an instance's manifest: its host routes,
// and the commands of its host apps to tell the frontend from the API.
func ResolveEntryFor(k *Knowledge, model interface{}, info *state.Manifest) Resolved {
	return resolveEntry(k, model, info.Routes, HostCommands(info))
}

// PickHostApp is the one answer to "which host app is the app" for the proxy
// route, the App chip, Open App and urls. In order: the recorded entry's
// service, the plan's app_service, a frontend dev server over the others (a
// tie goes to app, web, frontend, ui, client, and away from api, backend,
// server, worker), else the first by name. "" when there are no routes.
func PickHostApp(k *Knowledge, routes map[string]int, cmds map[string][]string) string {
	if len(routes) == 0 {
		return ""
	}
	names := make([]string, 0, len(routes))
	for n := range routes {
		names = append(names, n)
	}
	sort.Strings(names)
	if k != nil {
		if e := k.Entry; e != nil && !e.None && !e.Proposed {
			if _, ok := routes[e.Service]; ok {
				return e.Service
			}
		}
		if _, ok := routes[k.Plan.AppService]; ok && k.Plan.AppService != "" {
			return k.Plan.AppService
		}
	}
	cands := []string{}
	for _, n := range names {
		if isFrontendCommand(cmds[n]) {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 {
		cands = names
	}
	for _, want := range []string{"app", "web", "frontend", "ui", "client"} {
		for _, n := range cands {
			if n == want {
				return n
			}
		}
	}
	for _, n := range cands {
		switch n {
		case "api", "backend", "server", "worker":
		default:
			return n
		}
	}
	return cands[0]
}

var frontendTools = map[string]bool{
	"vite": true, "next": true, "nuxt": true, "nuxi": true, "astro": true, "webpack-dev-server": true,
	"react-scripts": true, "remix": true, "svelte-kit": true,
}

// isFrontendCommand reports whether argv runs a frontend dev server: vite,
// next, nuxt, astro, webpack serve or webpack-dev-server, react-scripts, ng
// serve, remix, svelte-kit. A tool matches by the base name of any argument,
// without a .js/.mjs/.cjs extension, so `node .../vite/bin/vite.js` counts.
func isFrontendCommand(argv []string) bool {
	for i, a := range argv {
		base := filepath.Base(a)
		for _, ext := range []string{".js", ".mjs", ".cjs"} {
			base = strings.TrimSuffix(base, ext)
		}
		switch {
		case frontendTools[base]:
			return true
		case (base == "webpack" || base == "ng") && i+1 < len(argv) && argv[i+1] == "serve":
			return true
		}
	}
	return false
}
