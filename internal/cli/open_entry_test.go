package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// entry-links task 4.4: open resolves app and link names before service names.
func TestOpenEntryAndLinks(t *testing.T) {
	v := newVerifyEnv(t, 4001, 4002, &knowledge.Entry{Service: "web", Port: 8080, Path: "/app"},
		[]knowledge.Link{{Name: "Dashboard", Path: "/admin"}})
	ps := func(svc, ports string) string {
		return `{"Labels":"io.stackfork.root=/x,com.docker.compose.project=` + v.project +
			`,com.docker.compose.service=` + svc + `","State":"running","Status":"","Ports":"` + ports + `"}`
	}
	t.Setenv("DOCKER_FAKE_PS", ps("web", "127.0.0.1:4001->8080/tcp")+"\n"+ps("mail", "127.0.0.1:4002->80/tcp")+"\n")
	open := func(args ...string) (string, string, int) {
		return runCLI(t, append(append(append([]string{}, v.flags...), "--root", v.root, "open"), args...)...)
	}
	h := "http://" + v.host
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--print"}, h + ":4001/app\n"},
		{[]string{"app", "--print"}, h + ":4001/app\n"},
		{[]string{"APP", "--print"}, h + ":4001/app\n"},
		{[]string{"dashboard", "--print"}, h + ":4001/admin\n"},
		{[]string{"--print", "Dashboard"}, h + ":4001/admin\n"},
		{[]string{"mail", "--print"}, h + ":4002\n"}, // a service, after the names
		{[]string{"web", "--print"}, h + ":4001/app\n"},
	} {
		out, stderr, code := open(tc.args...)
		if code != 0 || out != tc.want {
			t.Errorf("%v: %d %q %s, want %q", tc.args, code, out, stderr, tc.want)
		}
	}
	if _, stderr, code := open("nope", "--print"); code == 0 || !strings.Contains(stderr, "nope") {
		t.Errorf("unknown name: %d %s", code, stderr)
	}
}

// Bare `open` follows the entry rules: the App chip first (even when a
// service sorts before it), an error for a missing or `none` entry.
func TestOpenBareEntryRules(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry *knowledge.Entry
		want  string // stdout; empty means an error naming wantErr
		err   string
	}{
		{"guessed app chip", nil, "http://%s:4001\n", ""},
		{"recorded entry", &knowledge.Entry{Service: "web", Port: 8080}, "http://%s:4001\n", ""},
		{"recorded entry missing", &knowledge.Entry{Service: "gone", Port: 80}, "", "gone"},
		{"none", &knowledge.Entry{None: true}, "", "no web entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newVerifyEnv(t, 4001, 4002, tc.entry, nil)
			ps := func(svc, ports string) string {
				return `{"Labels":"io.stackfork.root=/x,com.docker.compose.project=` + v.project +
					`,com.docker.compose.service=` + svc + `","State":"running","Status":"","Ports":"` + ports + `"}`
			}
			t.Setenv("DOCKER_FAKE_PS", ps("web", "127.0.0.1:4001->8080/tcp")+"\n"+ps("mail", "127.0.0.1:4002->80/tcp")+"\n")
			out, stderr, code := runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "open", "--print")...)
			if tc.err != "" {
				if code == 0 || !strings.Contains(stderr, tc.err) {
					t.Fatalf("want error %q: %d %q %s", tc.err, code, out, stderr)
				}
				return
			}
			if want := fmt.Sprintf(tc.want, v.host); code != 0 || out != want {
				t.Fatalf("%d %q %s, want %q", code, out, stderr, want)
			}
		})
	}
}
