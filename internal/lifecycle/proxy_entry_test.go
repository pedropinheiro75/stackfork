package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const entryRouteModel = `{"services":{
 "web":{"ports":[{"target":3000,"published":"3000"}]},
 "nginx":{"ports":[{"target":80,"published":"8000"}]}}}`

const entryRoutePS = `{"ID":"a","Name":"web-1","Service":"web","State":"running","Health":"","Publishers":[{"Protocol":"tcp","PublishedPort":43000,"TargetPort":3000}]}
{"ID":"b","Name":"nginx-1","Service":"nginx","State":"running","Health":"","Publishers":[{"Protocol":"tcp","PublishedPort":48000,"TargetPort":80}]}
`

// routeFor builds one instance (manifest, snapshot, optional knowledge) with a
// fake docker that reports both services running, and returns the target the
// proxy would route its hostname to ("" for no route).
func routeFor(t *testing.T, k *knowledge.Knowledge, routes map[string]int) string {
	t.Helper()
	return routeForRaw(t, k, routes, "")
}

// routeForRaw is routeFor with raw, when set, as the knowledge.json bytes.
func routeForRaw(t *testing.T, k *knowledge.Knowledge, routes map[string]int, raw string) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' '" + entryRoutePS + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	base := t.TempDir()
	root := t.TempDir()
	if k != nil {
		if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
			t.Fatal(err)
		}
	}
	if raw != "" {
		if err := os.MkdirAll(filepath.Join(root, ".stackfork"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".stackfork", "knowledge.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(base, "sf-acme-web-1f")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.json"), []byte(entryRouteModel), 0o600); err != nil {
		t.Fatal(err)
	}
	m := state.Manifest{Project: "sf-acme-web-1f", Root: root, Branch: "main", Routes: routes}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	got, err := GatherRoutes(base, t.TempDir(), "bridge")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		return ""
	}
	if len(got) != 1 || got[0].Hostname != "acme-web-1f.localhost" {
		t.Fatalf("%+v", got)
	}
	return got[0].Target
}

// entry-links task 4.1: the proxy obeys the resolver for each source.
func TestGatherRoutesFollowsEntry(t *testing.T) {
	cases := map[string]struct {
		k      *knowledge.Knowledge
		routes map[string]int
		suffix string // the host port the route ends in; "" = no route
	}{
		"guessed, no knowledge": {nil, nil, ":48000"}, // 80 outranks 3000
		"recorded":              {&knowledge.Knowledge{Entry: &knowledge.Entry{Service: "web", Port: 3000}}, nil, ":43000"},
		"recorded missing":      {&knowledge.Knowledge{Entry: &knowledge.Entry{Service: "caddy", Port: 80}}, nil, ""},
		"none":                  {&knowledge.Knowledge{Entry: &knowledge.Entry{None: true}}, nil, ""},
		"proposed":              {&knowledge.Knowledge{Entry: &knowledge.Entry{Service: "web", Port: 3000, Proposed: true}}, nil, ":48000"},
		"host route":            {&knowledge.Knowledge{Entry: &knowledge.Entry{Service: "web", Port: 3000}}, map[string]int{"app": 41000}, ":41000"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := routeFor(t, tc.k, tc.routes)
			if tc.suffix == "" && got != "" || !strings.HasSuffix(got, tc.suffix) || (tc.suffix != "" && got == "") {
				t.Fatalf("target %q, want suffix %q", got, tc.suffix)
			}
		})
	}
}

// A knowledge file that cannot load counts as no entry, and the proxy says so.
func TestInstanceRouteLogsBrokenKnowledge(t *testing.T) {
	var buf strings.Builder
	old := routeWarnings
	routeWarnings = &buf
	defer func() { routeWarnings = old }()
	routeForRaw(t, nil, nil, "{nope")
	if !strings.Contains(buf.String(), "knowledge cannot be loaded") {
		t.Fatalf("log %q", buf.String())
	}
}
