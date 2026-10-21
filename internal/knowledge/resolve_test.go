package knowledge

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func modelOf(t *testing.T, doc string) interface{} {
	t.Helper()
	m, err := compose.DecodeJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const acmeModel = `{"services":{
 "web":{"ports":[{"target":3000,"published":"3000"}]},
 "nginx":{"ports":[{"target":80,"published":"8000"},{"target":443}]},
 "smtp4dev":{"image":"rnwood/smtp4dev","ports":[{"target":80,"published":"2525"}]},
 "worker":{"image":"acme/worker"}}}`

// Task 2.1: one row per source of D6.
func TestResolveEntry(t *testing.T) {
	model := modelOf(t, acmeModel)
	rec := &Knowledge{Entry: &Entry{Service: "nginx", Port: 80, Path: "/app"}}
	for name, tc := range map[string]struct {
		k               *Knowledge
		model           interface{}
		routes          map[string]int
		source, service string
		port            int
		ok              bool
		path, warn      string
	}{
		"host route":            {rec, model, map[string]int{"app": 41000}, "host-route", "", 41000, true, "/app", ""},
		"host route sorted":     {rec, model, map[string]int{"b": 2, "a": 1}, "host-route", "", 1, true, "/app", ""},
		"recorded":              {rec, model, nil, "recorded", "nginx", 80, true, "/app", ""},
		"recorded default path": {&Knowledge{Entry: &Entry{Service: "web", Port: 3000}}, model, nil, "recorded", "web", 3000, true, "/", ""},
		"missing service":       {&Knowledge{Entry: &Entry{Service: "caddy", Port: 80}}, model, nil, "recorded", "caddy", 80, false, "/", "the recorded entry caddy:80 is not in this stack; record the right one with stackfork learn record entry"},
		"undeclared port":       {&Knowledge{Entry: &Entry{Service: "nginx", Port: 81}}, model, nil, "recorded", "nginx", 81, false, "/", "the recorded entry nginx:81 is not in this stack; record the right one with stackfork learn record entry"},
		"none":                  {&Knowledge{Entry: &Entry{None: true}}, model, nil, "none", "", 0, false, "/", ""},
		"host route beats none": {&Knowledge{Entry: &Entry{None: true}}, model, map[string]int{"app": 1}, "host-route", "", 1, true, "/", ""},
		"proposed":              {&Knowledge{Entry: &Entry{Service: "worker", Port: 9, Proposed: true}}, model, nil, "guessed", "nginx", 80, true, "/", ""},
		"nil knowledge":         {nil, model, nil, "guessed", "nginx", 80, true, "/", ""},
		"no entry key":          {&Knowledge{}, model, nil, "guessed", "nginx", 80, true, "/", ""},
		"nil model":             {nil, nil, nil, "guessed", "", 0, false, "/", ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := ResolveEntry(tc.k, tc.model, tc.routes)
			if r.Source != tc.source || r.Service != tc.service || r.Port != tc.port || r.OK != tc.ok || r.Path != tc.path || r.Warning != tc.warn {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestResolveEntryHTTPS(t *testing.T) {
	model := modelOf(t, acmeModel)
	r := ResolveEntry(&Knowledge{Entry: &Entry{Service: "nginx", Port: 443}}, model, nil)
	if !r.HTTPS || !r.OK {
		t.Fatalf("%+v", r)
	}
	labelled := modelOf(t, `{"services":{"app":{"labels":{"io.stackfork.https":"8443"},"ports":[{"target":8443}]}}}`)
	if r := ResolveEntry(&Knowledge{Entry: &Entry{Service: "app", Port: 8443}}, labelled, nil); !r.HTTPS {
		t.Fatalf("%+v", r)
	}
}

func TestResolveLinks(t *testing.T) {
	model := modelOf(t, acmeModel)
	k := &Knowledge{
		Entry: &Entry{Service: "nginx", Port: 80, Path: "/app"},
		Links: []Link{
			{Name: "Dashboard", Path: "/admin"},
			{Name: "Email", Service: "smtp4dev", Port: 80},
			{Name: "Alt", Port: 443},
			{Name: "Gone", Service: "mailpit", Port: 8025},
			{Name: "BadPort", Service: "smtp4dev", Port: 25},
		},
	}
	r := ResolveEntry(k, model, nil)
	if len(r.Links) != 5 {
		t.Fatalf("%+v", r.Links)
	}
	d, e, alt, gone, bad := r.Links[0], r.Links[1], r.Links[2], r.Links[3], r.Links[4]
	if !d.OK || !d.OnEntry || d.Path != "/admin" || d.Service != "nginx" || d.Port != 80 {
		t.Errorf("dashboard %+v", d)
	}
	if !e.OK || e.OnEntry || e.Service != "smtp4dev" || e.Port != 80 || e.Path != "/" {
		t.Errorf("email %+v", e)
	}
	if !alt.OK || alt.Service != "nginx" || alt.Port != 443 || !alt.HTTPS || alt.OnEntry {
		t.Errorf("alt %+v", alt)
	}
	for _, l := range []ResolvedLink{gone, bad} {
		if l.OK || !strings.Contains(l.Warning, `"`+l.Name+`"`) || !strings.Contains(l.Warning, "learn record link") {
			t.Errorf("%+v", l)
		}
	}
	// The host route stands in for the entry; path links still apply.
	h := ResolveEntry(k, model, map[string]int{"app": 41000})
	if !h.Links[0].OK || h.Links[0].Port != 41000 || h.Links[2].OK {
		t.Errorf("%+v", h.Links)
	}
}

func TestResolvePathLinkWithoutEntry(t *testing.T) {
	model := modelOf(t, acmeModel)
	for name, k := range map[string]*Knowledge{
		"none":     {Entry: &Entry{None: true}, Links: []Link{{Name: "Docs", Path: "/d"}}},
		"no entry": {Links: []Link{{Name: "Docs", Path: "/d"}}},
	} {
		r := ResolveEntry(k, model, nil)
		if len(r.Links) != 1 || r.Links[0].OK || !strings.Contains(r.Links[0].Warning, "no entry") {
			t.Errorf("%s: %+v", name, r.Links)
		}
	}
	// A service link needs no entry.
	k := &Knowledge{Entry: &Entry{None: true}, Links: []Link{{Name: "Email", Service: "smtp4dev", Port: 80}}}
	if r := ResolveEntry(k, model, nil); !r.Links[0].OK {
		t.Errorf("%+v", r.Links)
	}
}

func row(service string, target, published int) compose.StatusRow {
	pub := map[string]interface{}{"TargetPort": jsonNum(target), "Protocol": "tcp"}
	if published > 0 {
		pub["PublishedPort"] = jsonNum(published)
	}
	return compose.StatusRow{Service: service, State: "running", Publishers: []map[string]interface{}{pub}}
}

// Task 2.2.
func TestURLBuilder(t *testing.T) {
	model := modelOf(t, acmeModel)
	k := &Knowledge{
		Entry: &Entry{Service: "nginx", Port: 80, Path: "/app"},
		Links: []Link{{Name: "Dashboard", Path: "/admin"}, {Name: "Email", Service: "smtp4dev", Port: 80}, {Name: "Root", Path: "/"}},
	}
	r := ResolveEntry(k, model, nil)
	const host = "acme-web-1f.localhost"
	t.Run("stable port from rows", func(t *testing.T) {
		w := Where{Hostname: host, Rows: []compose.StatusRow{row("nginx", 80, 28928), row("smtp4dev", 80, 28931)}}
		url, direct := r.AppURL(w)
		if url != "http://acme-web-1f.localhost:28928/app" || direct != "" {
			t.Fatalf("%q %q", url, direct)
		}
		if u, _ := r.LinkURL(r.Links[0], w); u != "http://acme-web-1f.localhost:28928/admin" {
			t.Fatalf("%q", u)
		}
		if u, _ := r.LinkURL(r.Links[1], w); u != "http://acme-web-1f.localhost:28931" {
			t.Fatalf("%q", u)
		}
		if u, _ := r.LinkURL(r.Links[2], w); u != "http://acme-web-1f.localhost:28928" {
			t.Fatalf("%q", u)
		}
	})
	t.Run("proxy routes the entry only", func(t *testing.T) {
		w := Where{Hostname: host, ProxyPort: 80, Rows: []compose.StatusRow{row("nginx", 80, 28928), row("smtp4dev", 80, 28931)}}
		url, direct := r.AppURL(w)
		if url != "http://acme-web-1f.localhost/app" || direct != "http://acme-web-1f.localhost:28928/app" {
			t.Fatalf("%q %q", url, direct)
		}
		if u, _ := r.LinkURL(r.Links[0], w); u != "http://acme-web-1f.localhost/admin" {
			t.Fatalf("%q", u)
		}
		if u, d := r.LinkURL(r.Links[1], w); u != "http://acme-web-1f.localhost:28931" || d != "" {
			t.Fatalf("%q %q", u, d)
		}
		w.ProxyPort = 8080
		if u, _ := r.AppURL(w); u != "http://acme-web-1f.localhost:8080/app" {
			t.Fatalf("%q", u)
		}
	})
	t.Run("random port, stopped", func(t *testing.T) {
		if u, _ := r.AppURL(Where{Hostname: host}); u != "" {
			t.Fatalf("%q", u)
		}
		if u, _ := r.LinkURL(r.Links[1], Where{Hostname: host}); u != "" {
			t.Fatalf("%q", u)
		}
	})
	t.Run("stable port, stopped", func(t *testing.T) {
		w := Where{Hostname: host, Stable: []state.PortAlloc{{Service: "nginx", Target: 80, Host: 28928}}}
		if u, _ := r.AppURL(w); u != "http://acme-web-1f.localhost:28928/app" {
			t.Fatalf("%q", u)
		}
	})
	t.Run("host route", func(t *testing.T) {
		h := ResolveEntry(k, model, map[string]int{"app": 41000})
		if u, _ := h.AppURL(Where{Hostname: host}); u != "http://127.0.0.1:41000/app" {
			t.Fatalf("%q", u)
		}
	})
	t.Run("https and unusable", func(t *testing.T) {
		s := ResolveEntry(&Knowledge{Entry: &Entry{Service: "nginx", Port: 443}}, model, nil)
		w := Where{Hostname: host, Rows: []compose.StatusRow{row("nginx", 443, 28929)}}
		if u, _ := s.AppURL(w); u != "https://acme-web-1f.localhost:28929" {
			t.Fatalf("%q", u)
		}
		bad := ResolveEntry(&Knowledge{Entry: &Entry{Service: "caddy", Port: 80}}, model, nil)
		if u, _ := bad.AppURL(w); u != "" {
			t.Fatalf("%q", u)
		}
	})
}

func jsonNum(n int) json.Number { return json.Number(strconv.Itoa(n)) }
