package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

var (
	apiCmd  = []string{"node", "--import=tsx", "src/app.ts"}
	viteCmd = []string{"node", "/repo/node_modules/vite/bin/vite.js", "--host", "0.0.0.0"}
)

func asApps(cmds map[string][]string) map[string]HostApp {
	out := map[string]HostApp{}
	for n, c := range cmds {
		out[n] = HostApp{Argv: c}
	}
	return out
}

func TestPickHostApp(t *testing.T) {
	routes := map[string]int{"api": 27882, "app": 28605}
	cmds := map[string][]string{"api": apiCmd, "app": viteCmd}
	for name, tc := range map[string]struct {
		k      *Knowledge
		routes map[string]int
		cmds   map[string][]string
		want   string
	}{
		"recorded entry names the host app": {&Knowledge{Entry: &Entry{Service: "api", Port: 1}}, routes, cmds, "api"},
		"proposed entry is ignored":         {&Knowledge{Entry: &Entry{Service: "api", Port: 1, Proposed: true}}, routes, cmds, "app"},
		"plan app_service":                  {&Knowledge{Plan: Plan{AppService: "api"}}, routes, nil, "api"},
		"entry beats app_service":           {&Knowledge{Entry: &Entry{Service: "app", Port: 1}, Plan: Plan{AppService: "api"}}, routes, cmds, "app"},
		"frontend dev server beats api":     {nil, routes, cmds, "app"},
		"two frontends: preferred name":     {nil, map[string]int{"admin": 1, "web": 2}, map[string][]string{"admin": {"next", "dev"}, "web": {"nuxt", "dev"}}, "web"},
		"no command known: app over api":    {nil, routes, nil, "app"},
		"no command, nothing preferred":     {nil, map[string]int{"api": 1, "worker": 2}, nil, "api"},
		"unpreferred name beats denied one": {nil, map[string]int{"api": 1, "docs": 2}, nil, "docs"},
		"one app":                           {nil, map[string]int{"only": 1}, nil, "only"},
		"no apps":                           {nil, nil, nil, ""},
	} {
		if got := PickHostApp(tc.k, tc.routes, asApps(tc.cmds)); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestIsFrontendCommand(t *testing.T) {
	for _, c := range []string{"vite", "bunx vite --host", "next dev", "nuxt dev", "astro dev", "webpack serve", "webpack-dev-server", "react-scripts start", "ng serve", "remix dev", "svelte-kit dev", "node /x/node_modules/vite/bin/vite.js --host 0.0.0.0"} {
		if !isFrontendApp(HostApp{Argv: strings.Fields(c)}) {
			t.Errorf("%q should count as a frontend dev server", c)
		}
	}
	for _, c := range []string{"node --import=tsx src/app.ts", "uvicorn main:app", "go run ./cmd/api", "webpack build"} {
		if isFrontendApp(HostApp{Argv: strings.Fields(c)}) {
			t.Errorf("%q should not", c)
		}
	}
}

func TestResolveEntryForHostRoutePicksTheFrontend(t *testing.T) {
	m := &state.Manifest{Routes: map[string]int{"api": 27882, "app": 28605}, HostProcs: map[string]state.HostProc{
		"api": {Restart: append([]string{"host", "--supervise", "api", "--"}, apiCmd...)},
		"app": {Cmdline: append([]string{"stackfork", "host", "--supervise", "app", "--"}, viteCmd...)},
	}}
	r := ResolveEntryFor(nil, modelOf(t, acmeModel), m)
	if r.Source != SourceHostRoute || r.Port != 28605 || r.HostApp != "app" {
		t.Errorf("%+v", r)
	}
}

// `pnpm dev` is a frontend only when the package.json of its directory depends on a dev server.
func TestPickHostAppReadsPackageJSON(t *testing.T) {
	root := t.TempDir()
	write := func(dir, deps string) {
		os.MkdirAll(filepath.Join(root, dir), 0o755)
		os.WriteFile(filepath.Join(root, dir, "package.json"), []byte(`{"devDependencies":{`+deps+`}}`), 0o644)
	}
	write("ui", `"vite":"^5"`)
	write("svc", `"express":"^4"`)
	routes := map[string]int{"aaa": 1, "zzz": 2}
	for _, runner := range [][]string{{"pnpm", "dev"}, {"bun", "run", "dev"}, {"yarn", "start"}, {"npm", "run", "serve"}} {
		apps := map[string]HostApp{"aaa": {Argv: runner, Dir: filepath.Join(root, "svc")}, "zzz": {Argv: runner, Dir: filepath.Join(root, "ui")}}
		if got := PickHostApp(nil, routes, apps); got != "zzz" {
			t.Errorf("%v: %q, the one whose package.json has vite", runner, got)
		}
	}
	apps := map[string]HostApp{"aaa": {Argv: []string{"pnpm", "dev"}, Dir: filepath.Join(root, "svc")}, "zzz": {Argv: []string{"pnpm", "dev"}, Dir: filepath.Join(root, "svc")}}
	if got := PickHostApp(nil, routes, apps); got != "aaa" {
		t.Errorf("no dev server dependency anywhere: name order, got %q", got)
	}
	write("fe", `"@angular/cli":"^17"`)
	if !isFrontendApp(HostApp{Argv: []string{"pnpm", "dev"}, Dir: filepath.Join(root, "fe")}) {
		t.Error("@angular/cli counts")
	}
	if isFrontendApp(HostApp{Argv: []string{"pnpm", "dev"}}) {
		t.Error("without a directory nothing is read")
	}
}
