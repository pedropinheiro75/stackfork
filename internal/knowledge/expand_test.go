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
		Services: map[string]ServiceAddr{"db": {URL: "postgres://127.0.0.1:20050", Port: 20050}},
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
		{"{{service.db.url}}", "postgres://127.0.0.1:20050"},
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
		"{{nope}}":              "{{nope}}",
		"{{ghost.port}}":        "{{ghost.port}}",
		"{{studio.port}}":       "{{studio.port}}",
		"{{service.cache.url}}": "{{service.cache.url}}",
		"{{service.db.host}}":   "{{service.db.host}}",
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

func TestExpandValuesAreVerbatimAndChecked(t *testing.T) {
	s := testScope()
	s.Services = map[string]ServiceAddr{"db": {URL: "x://a;$(id)", Port: 1}}
	if _, err := s.Expand([]string{"{{service.db.url}}"}); err == nil {
		t.Error("a url with shell characters must be refused")
	}
	s.Hostname = "evil.localhost;rm"
	if _, err := s.Expand([]string{"{{hostname}}"}); err == nil {
		t.Error("a hostname outside the DNS label charset must be refused")
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
