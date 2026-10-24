package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// HostApp is what the picker knows of a recorded host app: its command and
// the directory it runs in (absolute, inside the checkout; "" when unknown).
type HostApp struct {
	Argv []string
	Dir  string
}

// HostApps maps each recorded host app to its command (the part of its
// supervisor command line after "--") and working directory (its --dir).
func HostApps(info *state.Manifest) map[string]HostApp {
	out := map[string]HostApp{}
	for n, p := range info.HostProcs {
		args := p.Restart
		if len(args) == 0 && len(p.Cmdline) > 1 {
			args = p.Cmdline[1:]
		}
		var app HostApp
		for i, a := range args {
			if a == "--" {
				app.Argv = args[i+1:]
				break
			}
			if a == "--dir" && i+1 < len(args) && info.Root != "" {
				d := filepath.Join(info.Root, args[i+1])
				if rel, err := filepath.Rel(info.Root, d); err == nil && !strings.HasPrefix(rel, "..") {
					app.Dir = d
				}
			}
		}
		out[n] = app
	}
	return out
}

// ResolveEntryFor is ResolveEntry for an instance's manifest: its host routes,
// and the commands of its host apps to tell the frontend from the API.
func ResolveEntryFor(k *Knowledge, model interface{}, info *state.Manifest) Resolved {
	return resolveEntry(k, model, info.Routes, HostApps(info))
}

// PickHostApp is the one answer to "which host app is the app" for the proxy
// route, the App chip, Open App and urls. In order: the recorded entry's
// service, the plan's app_service, a frontend dev server over the others (a
// tie goes to app, web, frontend, ui, client, and away from api, backend,
// server, worker), else the first by name. "" when there are no routes.
func PickHostApp(k *Knowledge, routes map[string]int, apps map[string]HostApp) string {
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
		if isFrontendApp(apps[n]) {
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

// isFrontendApp reports whether the app runs a frontend dev server: vite,
// next, nuxt, astro, webpack serve or webpack-dev-server, react-scripts, ng
// serve, remix, svelte-kit. A tool matches by the base name of any argument,
// without a .js/.mjs/.cjs extension, so `node .../vite/bin/vite.js` counts.
func isFrontendApp(app HostApp) bool {
	argv := app.Argv
	if isPackageRunner(argv) {
		return dependsOnDevServer(app.Dir)
	}
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

// isPackageRunner matches `npm|pnpm|yarn|bun [run] dev|start|serve`: the
// command alone does not say what the script starts.
func isPackageRunner(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	switch filepath.Base(argv[0]) {
	case "npm", "pnpm", "yarn", "bun":
	default:
		return false
	}
	script := argv[1]
	if script == "run" && len(argv) > 2 {
		script = argv[2]
	}
	return script == "dev" || script == "start" || script == "serve"
}

var devServerPackages = []string{"vite", "next", "nuxt", "astro", "react-scripts", "@angular/cli", "@sveltejs/kit", "svelte-kit", "remix", "@remix-run/dev"}

// dependsOnDevServer reads dir/package.json, and only that file, for a
// dependency on a frontend dev server.
func dependsOnDevServer(dir string) bool {
	if dir == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return false
	}
	for _, name := range devServerPackages {
		if _, ok := pkg.Dependencies[name]; ok {
			return true
		}
		if _, ok := pkg.DevDependencies[name]; ok {
			return true
		}
	}
	return false
}
