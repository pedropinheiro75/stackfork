package dashboard

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

const hostileName = `<img src=x onerror=alert(1)>`

func running(i *inventory.Instance) { i.State = inventory.StateRunning }

var entryChipCases = []struct {
	name string
	inst inventory.Instance
	want []string
	not  []string
}{
	{"running entry and links", runInst(nil, func(i *inventory.Instance) {
		running(i)
		i.Apps = []inventory.AppLink{
			{Label: "App", Source: "entry", Running: true, URL: "http://acme.localhost:26607/app", Direct: "http://127.0.0.1:20060/app"},
			{Label: "Dashboard", Source: "link", Running: true, URL: "http://127.0.0.1:20061/admin"},
			{Label: "Email", Source: "link", Running: true, URL: "http://127.0.0.1:20062"},
		}
	}), []string{
		`<a class="app-chip app-chip-link" data-key="sf-acme-web-2ffa6841c75d/app/App" href="http://acme.localhost:26607/app"`,
		`aria-label="Open App in a new tab"`, `>App →</a>`,
		`aria-label="Open Dashboard in a new tab"`, `>Dashboard →</a>`, `>Email →</a>`,
		`data-key="sf-acme-web-2ffa6841c75d/app/Dashboard"`,
		`data-key="sf-acme-web-2ffa6841c75d/open" title="http://acme.localhost:26607/app">Open App</a>`,
	}, nil},
	{"stopped chips are off, no href", runInst(nil, func(i *inventory.Instance) {
		i.Apps = []inventory.AppLink{
			{Label: "App", Source: "entry", URL: "http://127.0.0.1:20060/app"},
			{Label: "Dashboard", Source: "link", URL: "http://127.0.0.1:20061"},
		}
	}), []string{`<span class="app-chip app-chip-link stopped" role="link" aria-disabled="true" tabindex="0" data-key="sf-acme-web-2ffa6841c75d/app/Dashboard" aria-label="Dashboard is not running" title="Starts with the stack">Dashboard →</span>`},
		[]string{`href="http://127.0.0.1:20061`, "Open App"}},
	{"missing target is a warning chip", runInst(nil, func(i *inventory.Instance) {
		running(i)
		i.Apps = []inventory.AppLink{
			{Label: "App", Source: "entry", Warning: "entry nginx:80 is not in this stack"},
			{Label: "Mail", Source: "link", Running: true, URL: "http://127.0.0.1:1025"},
		}
		i.Warnings = []string{"entry nginx:80 is not in this stack"}
	}), []string{`<span class="app-chip app-chip-link stopped app-chip-warn" role="link" aria-disabled="true" tabindex="0" data-key="sf-acme-web-2ffa6841c75d/app/App" aria-label="App unavailable: entry nginx:80 is not in this stack" title="entry nginx:80 is not in this stack"><span class="chip-glyph" aria-hidden="true">▲</span>App →</span>`,
		`data-callout="warnings"`},
		[]string{`>Open App</a>`}},
	{"hostile link name is escaped", runInst(nil, func(i *inventory.Instance) {
		running(i)
		i.Apps = []inventory.AppLink{{Label: hostileName, Source: "link", Running: true, URL: "http://127.0.0.1:1/x"}}
	}), []string{`&lt;img src=x onerror=alert(1)&gt; →</a>`, `aria-label="Open &lt;img src=x onerror=alert(1)&gt; in a new tab"`},
		[]string{"<img src=x"}},
	{"guessed app chip keeps the old markup", runInst(nil, func(i *inventory.Instance) {
		running(i)
		i.Apps = []inventory.AppLink{{Label: "App", Source: "service", Service: "web", Running: true, URL: "http://127.0.0.1:3000", Guessed: true}}
	}), []string{`>App</a>`}, []string{"App →"}},
}

func init() {
	for _, c := range entryChipCases {
		runCardCases = append(runCardCases, struct {
			name string
			inst inventory.Instance
			want []string
			not  []string
		}{"entry chips: " + c.name, c.inst, c.want, c.not})
	}
}

// Open App follows the entry; a link chip never stands in for a missing entry.
func TestOpenAppPrefersEntry(t *testing.T) {
	link := inventory.AppLink{Label: "Dashboard", Source: "link", Running: true, URL: "http://l"}
	svc := inventory.AppLink{Label: "App", Source: "service", Running: true, URL: "http://s"}
	ent := inventory.AppLink{Label: "App", Source: "entry", Running: true, URL: "http://e"}
	if a := openApp([]inventory.AppLink{link, ent, svc}, false); a == nil || a.URL != "http://e" {
		t.Fatalf("entry first: %+v", a)
	}
	if a := openApp([]inventory.AppLink{{Label: "App", Source: "entry", Warning: "w"}, link, svc}, false); a != nil {
		t.Fatalf("a missing entry must not fall back: %+v", a)
	}
	if a := openApp([]inventory.AppLink{link, svc}, false); a == nil || a.URL != "http://s" {
		t.Fatalf("guessed strip: %+v", a)
	}
	if a := openApp([]inventory.AppLink{svc}, true); a != nil {
		t.Errorf("entry none must not guess: %+v", a)
	}
	if a := openApp([]inventory.AppLink{link}, false); a != nil {
		t.Fatalf("a link alone is not the app: %+v", a)
	}
}

// app.js's pickOpenApp picks what dot.go's openApp picks, for every shape of
// app strip.
func TestOpenAppScriptAgrees(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	link := inventory.AppLink{Label: "Dashboard", Source: "link", Running: true, URL: "http://l"}
	svc := inventory.AppLink{Label: "web", Source: "service", Running: true, URL: "http://s"}
	app := inventory.AppLink{Label: "App", Source: "service", Running: true, URL: "http://a", Guessed: true}
	mail := inventory.AppLink{Label: "Mail inbox", Source: "service", Running: true, URL: "http://m"}
	ent := inventory.AppLink{Label: "App", Source: "entry", Running: true, URL: "http://e"}
	stoppedEnt := inventory.AppLink{Label: "App", Source: "entry", URL: "http://e"}
	type pick struct {
		Apps []inventory.AppLink `json:"apps"`
		None bool                `json:"none"`
	}
	lists := []pick{}
	for _, apps := range [][]inventory.AppLink{
		{link, ent, svc}, {{Label: "App", Source: "entry", Warning: "w"}, link, svc}, {stoppedEnt, svc},
		{link, svc}, {link}, {mail, app}, {mail, svc}, {}, nil,
	} {
		lists = append(lists, pick{Apps: apps})
	}
	lists = append(lists, pick{Apps: []inventory.AppLink{mail, svc}, None: true})
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json")
	b, _ := json.Marshal(lists)
	os.WriteFile(in, b, 0o600)
	cmd := exec.Command(node, abs(t, "testdata/appjs_harness.js"), abs(t, "static/app.js"), abs(t, "testdata/open_app.case.js"))
	cmd.Env = append(os.Environ(), "SF_PARITY_IN="+in, "SF_PARITY_OUT="+out)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, o)
	}
	var js []string
	raw, _ := os.ReadFile(out)
	if err := json.Unmarshal(raw, &js); err != nil || len(js) != len(lists) {
		t.Fatalf("%v %s", err, raw)
	}
	for i, c := range lists {
		want := ""
		if a := openApp(c.Apps, c.None); a != nil {
			want = a.URL
		}
		if js[i] != want {
			t.Errorf("list %d: app.js picks %q, openApp %q", i, js[i], want)
		}
	}
}
