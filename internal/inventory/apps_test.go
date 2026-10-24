package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func writeAppsFixture(t *testing.T, base, project, root string, m *state.Manifest, config map[string]interface{}) {
	t.Helper()
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m.Project = project
	m.Root = root
	m.Branch = "main"
	if m.Hostname == "" {
		m.Hostname = project + ".localhost"
	}
	if m.Files == nil {
		m.Files = []string{"compose.yaml"}
	}
	if m.Inputs == nil {
		m.Inputs = map[string]string{}
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}
}

// TestAppsFromStoppedStablePortService covers task 3.8: a stopped stack
// with a stable-port HTTP service still shows the App chip for the address
// it will use once started.
func TestAppsFromStoppedStablePortService(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	config := map[string]interface{}{
		"services": map[string]interface{}{
			"web": map[string]interface{}{
				"image": "node:22",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("3000"), "published": "20050", "protocol": "tcp"},
				},
			},
		},
	}
	project := "sf-appsstopped-1"
	writeAppsFixture(t, newBase, project, root, &state.Manifest{
		HTTPPorts: []int{3000},
	}, config)

	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == project {
			inst = i
		}
	}
	if len(inst.Apps) != 1 {
		t.Fatalf("expected 1 app link, got %+v", inst.Apps)
	}
	app := inst.Apps[0]
	if app.Running {
		t.Errorf("expected Running=false for a stopped stack, got %+v", app)
	}
	if app.URL != "http://"+project+".localhost:20050" {
		t.Errorf("unexpected app URL: %+v", app)
	}
	if app.Label != "App" {
		t.Errorf("expected label App for the primary (only) HTTP service, got %q", app.Label)
	}
}

// TestAppsLegacyStoppedRandomPort covers the legacy/--random-ports case: no
// URL is fabricated, only the "port assigned on start" hint.
func TestAppsLegacyStoppedRandomPort(t *testing.T) {
	installFakeDocker(t)
	legacyBase := t.TempDir()
	root := gitRepo(t, "main")

	config := map[string]interface{}{
		"services": map[string]interface{}{
			"web": map[string]interface{}{
				"image": "node:22",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("3000"), "protocol": "tcp"},
				},
			},
		},
	}
	project := "idv-appslegacy-1"
	writeAppsFixture(t, legacyBase, project, root, &state.Manifest{HTTPPorts: []int{3000}}, config)

	inv, err := Collect(Options{NewStateBase: t.TempDir(), LegacyStateBase: legacyBase, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == project {
			inst = i
		}
	}
	if len(inst.Apps) != 1 {
		t.Fatalf("expected 1 app link, got %+v", inst.Apps)
	}
	if inst.Apps[0].URL != "" || inst.Apps[0].Hint != "port assigned on start" {
		t.Errorf("expected no URL and the assigned-on-start hint, got %+v", inst.Apps[0])
	}
}

// TestAppsFromRoute covers task 3.8's host-run route case: the real-world
// gap this whole feature fixes (a checkout whose app runs on the host).
func TestAppsFromRoute(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	config := map[string]interface{}{"services": map[string]interface{}{
		"keepalive": map[string]interface{}{"image": "alpine:3.20"},
	}}
	project := "sf-appsroute-1"
	writeAppsFixture(t, newBase, project, root, &state.Manifest{
		Routes: map[string]int{"web": 5173},
	}, config)

	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == project {
			inst = i
		}
	}
	if len(inst.Apps) != 1 {
		t.Fatalf("expected 1 app link, got %+v", inst.Apps)
	}
	app := inst.Apps[0]
	if app.Source != "route" || app.URL != "http://"+inst.Hostname+":5173" || !app.Running {
		t.Errorf("unexpected route app link: %+v", app)
	}
}

// TestAppsFromKnowledgeOnlyWhenNothingElseQualifies covers the fallback
// gate: a knowledge-derived hint appears only when the stack has no
// service or route app link at all.
func TestAppsFromKnowledgeOnlyWhenNothingElseQualifies(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	// A service with no HTTP port at all (e.g. a bare keepalive), and no
	// route: nothing else in the strip has anything to open.
	config := map[string]interface{}{"services": map[string]interface{}{
		"keepalive": map[string]interface{}{"image": "alpine:3.20"},
	}}
	project := "sf-appsknowledge-1"
	writeAppsFixture(t, newBase, project, root, &state.Manifest{}, config)

	k := &knowledge.Knowledge{
		Schema: knowledge.CurrentSchema, Status: knowledge.StatusDraft,
		Facts: knowledge.Facts{Scripts: []knowledge.Script{
			{Role: "dev", Name: "dev", Source: "package.json", Command: "npm run dev"},
			{Role: "dev", Name: "dev:web", Source: "package.json", Command: "npm run dev:web"},
			{Role: "dev", Name: "dev:api", Source: "package.json", Command: "npm run dev:api"},
			{Role: "dev", Name: "dev:docs", Source: "package.json", Command: "npm run dev:docs"},
			{Role: "dev", Name: "dev:ui", Source: "package.json", Command: "npm run dev:ui"},
		}},
	}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}

	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == project {
			inst = i
		}
	}
	if len(inst.Apps) != 1 {
		t.Fatalf("expected exactly 1 knowledge-derived app hint for 5 dev scripts, got %+v", inst.Apps)
	}
	if !inst.IsMain || inst.Used.IsZero() {
		t.Errorf("want IsMain and a Used time, got IsMain=%v Used=%v", inst.IsMain, inst.Used)
	}
	app := inst.Apps[0]
	if app.Source != "knowledge" || app.Running {
		t.Errorf("unexpected knowledge app link: %+v", app)
	}
	if app.Hint == "" {
		t.Errorf("expected a non-empty hint naming the dev script")
	}

	// Now add a route: the knowledge hint must disappear, since there's
	// something real to open instead.
	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.Routes = map[string]int{"web": 5173}
	if err := state.WritePrivate(filepath.Join(newBase, project, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	inv, err = Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range inv.Instances {
		if i.Project == project {
			inst = i
		}
	}
	if len(inst.Apps) != 1 || inst.Apps[0].Source != "route" {
		t.Fatalf("expected the route to replace the knowledge hint, got %+v", inst.Apps)
	}
}

// TestAppsCollapsesProxiedAndDirectIntoOneChip is the regression test for
// the duplicate "App" chips the maintainer found in the dashboard
// screenshots: once the shared proxy is running and routing a stack's
// primary HTTP service, that service carries both its own direct endpoint
// and the proxy's extra portless one (servicesForInstance), and buildApps
// must collapse that pair into a single chip - the proxied URL, with the
// direct one kept as AppLink.Direct for the template's tooltip - never two
// identically-labelled "App" chips.
func TestAppsCollapsesProxiedAndDirectIntoOneChip(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")

	config := map[string]interface{}{"services": map[string]interface{}{
		"web": map[string]interface{}{
			"image": "node:22",
			"ports": []interface{}{
				map[string]interface{}{"target": json.Number("3000"), "published": "20050", "protocol": "tcp"},
			},
		},
	}}
	project := "sf-appsproxied-1"
	writeAppsFixture(t, newBase, project, root, &state.Manifest{
		HTTPPorts: []int{3000},
	}, config)

	t.Setenv("DOCKER_FAKE_PS", psLine(t, project, "web", "running", "Up 1 minute", "127.0.0.1:20050->3000/tcp")+"\n")
	t.Setenv("DOCKER_FAKE_PROXY_RUNNING", "true")
	if err := proxy.WritePort(newBase, 18089); err != nil {
		t.Fatal(err)
	}

	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == project {
			inst = i
		}
	}
	if len(inst.Apps) != 1 {
		t.Fatalf("expected exactly 1 App chip (proxied+direct collapsed), got %d: %+v", len(inst.Apps), inst.Apps)
	}
	app := inst.Apps[0]
	if app.Label != "App" {
		t.Errorf("expected label App, got %q", app.Label)
	}
	wantURL := "http://" + project + ".localhost:18089"
	if app.URL != wantURL {
		t.Errorf("expected the proxied URL to win, got %q want %q", app.URL, wantURL)
	}
	wantDirect := "http://" + project + ".localhost:20050"
	if app.Direct != wantDirect {
		t.Errorf("expected the direct URL kept as Direct, got %q want %q", app.Direct, wantDirect)
	}
}

// TestBuildEnvBlockIncludesServiceURL covers add-env-and-seed's host-env
// capability: `stackfork env` reuses this exact block (inventory.Instance's
// own EnvBlock field), so every HTTP(S)-serving service must contribute its
// own <NAME>_URL line here, alongside DATABASE_URL/REDIS_URL/etc - a pure
// unit test of buildEnvBlock/bestHTTPEndpoint/envKeyForService, no Collect
// or docker needed.
func TestBuildEnvBlockIncludesServiceURL(t *testing.T) {
	services := []Service{
		{
			Name: "db", Kind: "postgres", State: "running",
			ConnectionURL: "postgres://postgres:<password>@127.0.0.1:20050/app",
		},
		{
			Name: "web-app", Kind: "", State: "running",
			Endpoints: []Endpoint{
				{Scheme: "http", URL: "http://myapp.localhost:20051", Container: 3000, Known: true},
			},
		},
	}
	block := buildEnvBlock(services)
	lines := strings.Split(block, "\n")
	want := map[string]string{
		"DATABASE_URL": "postgres://postgres:<password>@127.0.0.1:20050/app",
		"WEB_APP_URL":  "http://myapp.localhost:20051",
	}
	got := map[string]string{}
	for _, l := range lines {
		k, v, ok := strings.Cut(l, "=")
		if ok {
			got[k] = v
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("expected %s=%q, got %+v", k, v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("expected exactly %d lines, got %+v", len(want), got)
	}
}

// TestBuildEnvBlockPrefersProxiedServiceURL: the same proxied-preferred
// pairing rule buildApps uses for App chips (bestHTTPEndpoint shares it).
func TestBuildEnvBlockPrefersProxiedServiceURL(t *testing.T) {
	services := []Service{{
		Name: "app", State: "running",
		Endpoints: []Endpoint{
			{Scheme: "http", URL: "http://app.localhost:20051", Container: 3000, Known: true},
			{Scheme: "http", URL: "http://app.localhost:18089", Container: 3000, Known: true, Proxied: true},
		},
	}}
	block := buildEnvBlock(services)
	if !strings.Contains(block, "APP_URL=http://app.localhost:18089") {
		t.Errorf("expected the proxied address to win in APP_URL, got %q", block)
	}
	if strings.Contains(block, "20051") {
		t.Errorf("expected only the proxied address, direct one leaked into the block: %q", block)
	}
}
