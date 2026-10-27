package knowledge

import (
	"reflect"
	"strings"
	"testing"
)

func testScope() Scope {
	return Scope{
		Self: "app", Hostname: "feat-1.localhost",
		Apps:     map[string]int{"api": 14003, "app": 13003, "studio": 0},
		Services: map[string]ServiceAddr{"db": {URL: "127.0.0.1:20050", Port: 20050}},
	}
}

func TestExpandPlaceholders(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"{{port}}", "13003"},
		{"{{url}}", "http://feat-1.localhost:13003"},
		{"{{hostname}}", "feat-1.localhost"},
		{"{{api.port}}", "14003"},
		{"{{api.url}}", "http://feat-1.localhost:14003"},
		{"{{service.db.port}}", "20050"},
		{"{{service.db.url}}", "127.0.0.1:20050"},
		{"{{ port }}", "13003"},
		{"--port={{port}}", "--port=13003"},
		{"UI_URL={{app.url}}", "UI_URL=http://feat-1.localhost:13003"},
		{"{{port}}:{{api.port}}", "13003:14003"},
		{"plain {{ not a placeholder }}", "plain {{ not a placeholder }}"},
	}
	for _, c := range cases {
		got, err := testScope().Expand([]string{"env", c.in})
		if err != nil || !reflect.DeepEqual(got, []string{"env", c.want}) {
			t.Errorf("%q: got %q err %v, want %q", c.in, got, err, c.want)
		}
	}
}

func TestExpandErrorsNameThePlaceholder(t *testing.T) {
	for in, want := range map[string]string{
		"{{ghost.port}}":        "{{ghost.port}}",
		"{{studio.port}}":       "{{studio.port}}",
		"{{service.cache.url}}": "{{service.cache.url}}",
	} {
		got, err := testScope().Expand([]string{"x", in})
		if err == nil || !strings.Contains(err.Error(), want) || got != nil {
			t.Errorf("%q: got %q err %v", in, got, err)
		}
	}
	s := testScope()
	s.Self = ""
	if _, err := s.Expand([]string{"{{port}}"}); err == nil || !strings.Contains(err.Error(), "{{port}}") {
		t.Errorf("{{port}} without a host app: %v", err)
	}
	if _, err := s.Expand([]string{"{{api.port}}"}); err != nil {
		t.Errorf("{{api.port}} in an exec step: %v", err)
	}
}

func TestExpandValuesStayLiteralArguments(t *testing.T) {
	s := testScope()
	s.Services = map[string]ServiceAddr{"db": {URL: "x://a;$(id){{port}}", Port: 1}}
	got, err := s.Expand([]string{"echo", "{{service.db.url}}"})
	if err != nil || !reflect.DeepEqual(got, []string{"echo", "x://a;$(id){{port}}"}) {
		t.Fatalf("got %q err %v", got, err)
	}
	s.Hostname = "evil.localhost;rm"
	if _, err := s.Expand([]string{"{{hostname}}"}); err == nil {
		t.Error("a hostname outside the DNS label charset must be refused")
	}
	if _, err := s.Expand([]string{"{{url}}"}); err == nil {
		t.Error("{{url}} must check the hostname too")
	}
}

func TestHostLiteralWarnings(t *testing.T) {
	host := func(s Step) []string { return HostLiteralWarnings(s, "feat-1.localhost") }
	if w := host(Step{ID: "a", Kind: KindHost, Name: "a", Cmd: "vite --port {{port}}"}); len(w) != 0 {
		t.Errorf("placeholders warn: %v", w)
	}
	for _, s := range []Step{
		{ID: "a", Kind: KindHost, Name: "a", Cmd: "vite", Port: 13003},
		{ID: "a", Kind: KindHost, Name: "a", Cmd: "vite --port 13003"},
		{ID: "a", Kind: KindHost, Name: "a", Cmd: "vite --port=13003"},
		{ID: "a", Kind: KindHost, Name: "a", Cmd: "env UI_URL=http://feat-1.localhost:13003 vite"},
		{ID: "a", Kind: KindHost, Name: "a", Cmd: "env UI_URL=http://localhost:13003 vite"},
		{ID: "m", Kind: KindExec, On: OnHost, Cmd: "prisma studio --port 15555"},
		{ID: "m", Kind: KindExec, On: OnHost, Cmd: "echo feat-1.localhost"},
	} {
		w := host(s)
		if len(w) != 1 || !strings.Contains(w[0], "{{") {
			t.Errorf("%+v: warnings %q", s, w)
		}
	}
	if w := host(Step{ID: "m", Kind: KindExec, Cmd: "psql --port 5432"}); len(w) != 0 {
		t.Errorf("a container exec step warns: %v", w)
	}
	if w := host(Step{ID: "a", Kind: KindHost, Name: "a", Cmd: "node -e 1 2024"}); len(w) != 0 {
		t.Errorf("a bare number warns: %v", w)
	}
}

// Only the grammar expands; templates of other tools pass through untouched.
func TestExpandLeavesOtherTemplatesAlone(t *testing.T) {
	for _, in := range []string{"{{.Names}}", "{{ .Values.port }}", "{{json .}}", "{{.State.Health.Status}}", "{{nope}}", "{{service.db.host}}", "{{a.b.c}}", "{{ not a placeholder }}"} {
		got, err := testScope().Expand([]string{in})
		if err != nil || got[0] != in {
			t.Errorf("%q: got %q err %v", in, got, err)
		}
	}
}

func TestExpandRefusesUnsafeValues(t *testing.T) {
	for _, u := range []string{"a b", "a\tb", "a\nb", "-x", "a\x1bb"} {
		s := testScope()
		s.Services = map[string]ServiceAddr{"db": {URL: u, Port: 1}}
		if _, err := s.Expand([]string{"{{service.db.url}}"}); err == nil {
			t.Errorf("%q must be refused", u)
		}
	}
}

func TestNearMissPlaceholders(t *testing.T) {
	for in, want := range map[string]int{"vite --port {{prot}}": 1, "x {{app.prot}}": 1, "{{port}} {{api.url}}": 0, "{{.Names}}": 0, "{{ .Values.port }}": 0, "{{json .}}": 0} {
		w := 0
		for _, m := range HostLiteralWarnings(Step{ID: "a", Kind: KindHost, Name: "a", Cmd: in}, "") {
			if strings.Contains(m, "does not expand") {
				w++
			}
		}
		if w != want {
			t.Errorf("%q: %d near-miss warnings, want %d", in, w, want)
		}
	}
}

func TestExpandUsesProxyURLForTheServedApp(t *testing.T) {
	sc := testScope()
	sc.ProxyApp, sc.ProxyURL = "app", ProxyURL("feat-1.localhost", false, 80)
	for in, want := range map[string]string{
		"{{url}}": "http://feat-1.localhost", "{{app.url}}": "http://feat-1.localhost",
		"{{api.url}}": "http://feat-1.localhost:14003", "{{app.port}}": "13003", "{{hostname}}": "feat-1.localhost",
	} {
		if got, err := sc.Expand([]string{in}); err != nil || got[0] != want {
			t.Errorf("%s: got %q err %v, want %q", in, got, err, want)
		}
	}
	sc.ProxyURL = ProxyURL("feat-1.localhost", false, 8080)
	if got, _ := sc.Expand([]string{"{{app.url}}"}); got[0] != "http://feat-1.localhost:8080" {
		t.Errorf("proxy port: %q", got[0])
	}
}

func TestUsesProxyURL(t *testing.T) {
	for _, c := range []struct {
		cmd, self string
		want      bool
	}{
		{"env UI_URL={{app.url}} node", "api", true},
		{"env V={{api.url}} vite", "app", false},
		{"vite --origin {{url}}", "app", true},
		{"node --origin {{url}}", "api", false},
		{"node {{service.db.url}}", "api", true},
		{"node --port {{port}} {{app.port}}", "api", false},
	} {
		argv, _ := SplitArgv(c.cmd)
		if got := UsesProxyURL(argv, c.self, "app"); got != c.want {
			t.Errorf("%q (%s): got %v", c.cmd, c.self, got)
		}
	}
}
