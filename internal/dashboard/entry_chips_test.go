package dashboard

import (
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
	if a := openApp([]inventory.AppLink{link, ent, svc}); a == nil || a.URL != "http://e" {
		t.Fatalf("entry first: %+v", a)
	}
	if a := openApp([]inventory.AppLink{{Label: "App", Source: "entry", Warning: "w"}, link, svc}); a != nil {
		t.Fatalf("a missing entry must not fall back: %+v", a)
	}
	if a := openApp([]inventory.AppLink{link, svc}); a == nil || a.URL != "http://s" {
		t.Fatalf("guessed strip: %+v", a)
	}
	if a := openApp([]inventory.AppLink{link}); a != nil {
		t.Fatalf("a link alone is not the app: %+v", a)
	}
}
