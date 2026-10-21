package cli

import (
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
