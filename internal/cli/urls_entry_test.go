package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func urlsOut(t *testing.T, v verifyEnv) (string, string) {
	t.Helper()
	out, stderr, code := runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "urls")...)
	if code != 0 {
		t.Fatalf("urls: %d %s", code, stderr)
	}
	return out, stderr
}

// entry-links task 4.3: the entry and the links come first.
func TestURLsRecordedGuessedNone(t *testing.T) {
	links := []knowledge.Link{{Name: "Dashboard", Path: "/admin"}, {Name: "Email", Service: "mail", Port: 80}}
	for name, tc := range map[string]struct {
		entry *knowledge.Entry
		links []knowledge.Link
		want  func(h string) string
		warn  string
	}{
		"recorded": {&knowledge.Entry{Service: "web", Port: 8080, Path: "/app"}, links, func(h string) string {
			return fmt.Sprintf("App: http://%[1]s:4001/app (entry web:8080)\nDashboard: http://%[1]s:4001/admin\nEmail: http://%[1]s:4002 (mail:80)\nweb: http://%[1]s:4001 (8080/tcp)\nmail: http://%[1]s:4002 (80/tcp)\n", h)
		}, ""},
		"guessed": {nil, nil, func(h string) string {
			return fmt.Sprintf("App: http://%[1]s:4001 (guessed: web:8080; record the entry with stackfork learn record entry)\nweb: http://%[1]s:4001 (8080/tcp)\nmail: http://%[1]s:4002 (80/tcp)\n", h)
		}, ""},
		"proposed": {&knowledge.Entry{Service: "mail", Port: 80, Proposed: true}, nil, func(h string) string {
			return fmt.Sprintf("App: http://%[1]s:4001 (guessed: web:8080; record the entry with stackfork learn record entry)\nweb: http://%[1]s:4001 (8080/tcp)\nmail: http://%[1]s:4002 (80/tcp)\n", h)
		}, ""},
		"none": {&knowledge.Entry{None: true}, []knowledge.Link{{Name: "Email", Service: "mail", Port: 80}}, func(h string) string {
			return fmt.Sprintf("Email: http://%[1]s:4002 (mail:80)\nweb: http://%[1]s:4001 (8080/tcp)\nmail: http://%[1]s:4002 (80/tcp)\n", h)
		}, ""},
		"missing": {&knowledge.Entry{Service: "caddy", Port: 80}, nil, func(h string) string {
			return fmt.Sprintf("web: http://%[1]s:4001 (8080/tcp)\nmail: http://%[1]s:4002 (80/tcp)\n", h)
		}, "the recorded entry caddy:80 is not in this stack"},
	} {
		t.Run(name, func(t *testing.T) {
			v := newVerifyEnv(t, 4001, 4002, tc.entry, tc.links)
			out, stderr := urlsOut(t, v)
			if want := tc.want(v.host); out != want {
				t.Fatalf("got:\n%s\nwant:\n%s", out, want)
			}
			if !strings.Contains(stderr, tc.warn) {
				t.Fatalf("stderr %q lacks %q", stderr, tc.warn)
			}
		})
	}
}

func TestURLsRecordedEntryBehindProxy(t *testing.T) {
	v := newVerifyEnv(t, 4001, 4002, &knowledge.Entry{Service: "web", Port: 8080, Path: "/app"},
		[]knowledge.Link{{Name: "Dashboard", Path: "/admin"}, {Name: "Email", Service: "mail", Port: 80}})
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test-urls-entry")
	t.Setenv("DOCKER_FAKE_PROXY_RUNNING", "true")
	out, _ := urlsOut(t, v)
	want := fmt.Sprintf("App: http://%[1]s/app (entry web:8080)\nDashboard: http://%[1]s/admin\nEmail: http://%[1]s:4002 (mail:80)\n", v.host)
	if !strings.HasPrefix(out, want) {
		t.Fatalf("got:\n%s\nwant prefix:\n%s", out, want)
	}
}
