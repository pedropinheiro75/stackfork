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

func entryConfig() map[string]interface{} {
	port := func(target, published string) map[string]interface{} {
		return map[string]interface{}{"ports": []interface{}{
			map[string]interface{}{"target": json.Number(target), "published": published, "protocol": "tcp"}}}
	}
	nginx, mail, adminer := port("80", "28928"), port("80", "28931"), port("8080", "28940")
	nginx["image"], mail["image"], adminer["image"] = "nginx:alpine", "rnwood/smtp4dev", "adminer"
	return map[string]interface{}{"services": map[string]interface{}{"nginx": nginx, "smtp4dev": mail, "adminer": adminer}}
}

func collectEntry(t *testing.T, project string, k *knowledge.Knowledge) Instance {
	t.Helper()
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")
	writeAppsFixture(t, newBase, project, root, &state.Manifest{}, entryConfig())
	if k != nil {
		if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
			t.Fatal(err)
		}
	}
	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range inv.Instances {
		if i.Project == project {
			return i
		}
	}
	t.Fatal("instance not found")
	return Instance{}
}

func appsJSON(t *testing.T, apps []AppLink) string {
	t.Helper()
	b, err := json.Marshal(apps)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// entry-links task 4.2: the golden `apps` of an instance with an entry and
// links: entry first, links in recorded order, covered service chips gone.
func TestAppsFromRecordedEntryAndLinks(t *testing.T) {
	project := "sf-entryapps-1"
	inst := collectEntry(t, project, &knowledge.Knowledge{
		Entry: &knowledge.Entry{Service: "nginx", Port: 80, Path: "/app"},
		Links: []knowledge.Link{{Name: "Dashboard", Path: "/admin"}, {Name: "Email", Service: "smtp4dev", Port: 80}},
	})
	h := "http://" + project + ".localhost"
	want := `[{"label":"App","service":"nginx","url":"` + h + `:28928/app","source":"entry","running":false},` +
		`{"label":"Dashboard","service":"nginx","url":"` + h + `:28928/admin","source":"link","running":false},` +
		`{"label":"Email","service":"smtp4dev","url":"` + h + `:28931","source":"link","running":false},` +
		`{"label":"DB admin","service":"adminer","url":"` + h + `:28940","source":"service","running":false}]`
	if got := appsJSON(t, inst.Apps); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if len(inst.Warnings) != 0 {
		t.Fatalf("%v", inst.Warnings)
	}
}

func TestAppsFromRecordedEntryMissingService(t *testing.T) {
	project := "sf-entrymissing-1"
	inst := collectEntry(t, project, &knowledge.Knowledge{
		Entry: &knowledge.Entry{Service: "caddy", Port: 80},
		Links: []knowledge.Link{{Name: "Docs", Path: "/docs"}, {Name: "Email", Service: "smtp4dev", Port: 80}},
	})
	h := "http://" + project + ".localhost"
	warn := "the recorded entry caddy:80 is not in this stack; record the right one with stackfork learn record entry"
	want := `[{"label":"App","source":"entry","running":false,"warning":"` + warn + `"},` +
		`{"label":"Docs","source":"link","running":false,"warning":"link \"Docs\" is a path on the entry, which is not usable; record the right one with stackfork learn record link"},` +
		`{"label":"Email","service":"smtp4dev","url":"` + h + `:28931","source":"link","running":false},` +
		`{"label":"DB admin","service":"adminer","url":"` + h + `:28940","source":"service","running":false},` +
		`{"label":"nginx","service":"nginx","url":"` + h + `:28928","source":"service","running":false}]`
	if got := appsJSON(t, inst.Apps); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if len(inst.Warnings) != 2 || inst.Warnings[0] != warn {
		t.Fatalf("%v", inst.Warnings)
	}
}

func TestAppsEntryNone(t *testing.T) {
	inst := collectEntry(t, "sf-entrynone-1", &knowledge.Knowledge{Entry: &knowledge.Entry{None: true}})
	for _, a := range inst.Apps {
		if a.Label == "App" {
			t.Fatalf("none must show no App chip: %+v", inst.Apps)
		}
	}
	if len(inst.Apps) != 3 || len(inst.Warnings) != 0 {
		t.Fatalf("%+v %v", inst.Apps, inst.Warnings)
	}
}

// Without a recorded entry the strip is what it was before this change; the
// App chip is marked guessed only when a knowledge file exists.
func TestAppsWithoutRecordedEntryUnchanged(t *testing.T) {
	project := "sf-entryguess-1"
	h := "http://" + project + ".localhost"
	want := func(guessed string) string {
		return `[{"label":"DB admin","service":"adminer","url":"` + h + `:28940","source":"service","running":false},` +
			`{"label":"App","service":"nginx","url":"` + h + `:28928","source":"service","running":false` + guessed + `},` +
			`{"label":"Mail inbox","service":"smtp4dev","url":"` + h + `:28931","source":"service","running":false}]`
	}
	for name, tc := range map[string]struct {
		k       *knowledge.Knowledge
		guessed string
	}{
		"no file":  {nil, ""},
		"proposed": {&knowledge.Knowledge{Entry: &knowledge.Entry{Service: "adminer", Port: 8080, Proposed: true}}, `,"guessed":true`},
	} {
		inst := collectEntry(t, project, tc.k)
		if got := appsJSON(t, inst.Apps); got != want(tc.guessed) {
			t.Fatalf("%s:\ngot  %s\nwant %s", name, got, want(tc.guessed))
		}
	}
}

func TestAppsEntryRunningBehindProxy(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")
	project := "sf-entryproxy-1"
	writeAppsFixture(t, newBase, project, root, &state.Manifest{}, entryConfig())
	k := &knowledge.Knowledge{
		Entry: &knowledge.Entry{Service: "adminer", Port: 8080, Path: "/db"},
		Links: []knowledge.Link{{Name: "Docs", Path: "/docs"}, {Name: "Mail", Service: "smtp4dev", Port: 80}},
	}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_FAKE_PS",
		psLine(t, project, "adminer", "running", "Up 1 minute", "127.0.0.1:28940->8080/tcp")+"\n"+
			psLine(t, project, "smtp4dev", "running", "Up 1 minute", "127.0.0.1:28931->80/tcp")+"\n")
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
	h := "http://" + project + ".localhost"
	got := appsJSON(t, inst.Apps[:3])
	want := `[{"label":"App","service":"adminer","url":"` + h + `:18089/db","direct":"` + h + `:28940/db","source":"entry","running":true},` +
		`{"label":"Docs","service":"adminer","url":"` + h + `:18089/docs","direct":"` + h + `:28940/docs","source":"link","running":true},` +
		`{"label":"Mail","service":"smtp4dev","url":"` + h + `:28931","source":"link","running":true}]`
	if got != want || !strings.Contains(appsJSON(t, inst.Apps[3:]), `"nginx"`) {
		t.Fatalf("got  %s\nwant %s\nrest %+v", got, want, inst.Apps[3:])
	}
}

// A host route stands in for the recorded entry: one App chip, not two.
func TestAppsEntryHostRoute(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")
	project := "sf-entryroute-1"
	writeAppsFixture(t, newBase, project, root, &state.Manifest{Routes: map[string]int{"app": 41000}}, entryConfig())
	k := &knowledge.Knowledge{Entry: &knowledge.Entry{Service: "nginx", Port: 80, Path: "/app"}, Links: []knowledge.Link{{Name: "Docs", Path: "/docs"}}}
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
	got := appsJSON(t, inst.Apps[:2])
	want := `[{"label":"App","url":"http://` + inst.Hostname + `:41000/app","source":"entry","running":true},` +
		`{"label":"Docs","url":"http://` + inst.Hostname + `:41000/docs","source":"link","running":true}]`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, a := range inst.Apps {
		if a.Source == "route" {
			t.Fatalf("duplicate route chip: %+v", inst.Apps)
		}
	}
}

// A path link on a guessed entry must not hide the heuristic's App chip.
func TestAppsGuessedEntryKeepsAppWithPathLink(t *testing.T) {
	inst := collectEntry(t, "sf-entrypathlink-1", &knowledge.Knowledge{
		Entry: &knowledge.Entry{Service: "nginx", Port: 80, Proposed: true},
		Links: []knowledge.Link{{Name: "Dashboard", Path: "/admin"}},
	})
	var labels []string
	for _, a := range inst.Apps {
		labels = append(labels, a.Label)
	}
	if got := strings.Join(labels, ","); !strings.Contains(got, "App") || !strings.Contains(got, "Dashboard") {
		t.Fatalf("apps %s", got)
	}
}

// A knowledge file that cannot load counts as no entry, with a warning.
func TestAppsBrokenKnowledgeWarns(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")
	writeAppsFixture(t, newBase, "sf-entrybroken-1", root, &state.Manifest{}, entryConfig())
	if err := os.MkdirAll(filepath.Join(root, ".stackfork"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".stackfork", "knowledge.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range inv.Instances {
		if i.Project == "sf-entrybroken-1" {
			for _, w := range i.Warnings {
				if strings.HasPrefix(w, "knowledge cannot be loaded: ") {
					return
				}
			}
			t.Fatalf("warnings %v", i.Warnings)
		}
	}
	t.Fatal("instance not found")
}

// A link to a host app (hybrid) is running while its route is, and the route
// does not add a second chip for it.
func TestAppsLinkToHostAppRunsWithItsRoute(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")
	project := "sf-hostlink-1"
	writeAppsFixture(t, newBase, project, root, &state.Manifest{Routes: map[string]int{"app": 41000, "api": 41001}}, entryConfig())
	k := &knowledge.Knowledge{Entry: &knowledge.Entry{Service: "nginx", Port: 80}, Links: []knowledge.Link{{Name: "API", Service: "api", Port: 3001}}}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range inv.Instances {
		if i.Project != project {
			continue
		}
		var api *AppLink
		for j, a := range i.Apps {
			if a.Source == "route" && a.Label == "api" {
				t.Fatalf("duplicate route chip: %+v", i.Apps)
			}
			if a.Label == "API" {
				api = &i.Apps[j]
			}
		}
		if api == nil || !api.Running {
			t.Fatalf("API link must be running with its host route: %+v", i.Apps)
		}
		return
	}
	t.Fatal("instance not found")
}
