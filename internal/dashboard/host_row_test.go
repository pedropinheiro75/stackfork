package dashboard_test

import (
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// hostFixture adds a host process "db" (same name as the compose service
// "db") to the prepared fixture. live=true makes it a running one (this test
// process, with a bound port); otherwise a stale entry.
func hostFixture(t *testing.T, live bool) (srv *dashboard.Server, project, dir string) {
	t.Helper()
	newBase, legacyBase, project := preparedFixture(t)
	dir = filepath.Join(newBase, project)
	mp := filepath.Join(dir, "manifest.json")
	m, err := state.ReadManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	proc := state.HostProc{PID: 999999, StartedAt: "1970-01-01T00:00:00Z", Cmdline: []string{"x"}, Port: 40998}
	if live {
		start, err := state.ProcStart(os.Getpid())
		if err != nil {
			t.Skip("no process start time on this platform")
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		proc = state.HostProc{PID: os.Getpid(), StartedAt: start, Cmdline: os.Args, Port: ln.Addr().(*net.TCPAddr).Port}
	}
	m.HostProcs = map[string]state.HostProc{"db": proc}
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	srv, err = dashboard.New(newBase, legacyBase, "test", 55903)
	if err != nil {
		t.Fatal(err)
	}
	return srv, project, dir
}

func rowOf(t *testing.T, body, id string) string {
	t.Helper()
	i := strings.Index(body, `class="service-row" data-service="`+id+`"`)
	if i < 0 {
		t.Fatalf("row %s missing:\n%s", id, body)
	}
	return body[i : i+strings.Index(body[i:], `class="service-settings"`)]
}

func getPage(srv *dashboard.Server) string {
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55903"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestHostRowIsDistinctFromSameNamedComposeService(t *testing.T) {
	installFakeDocker(t)
	srv, _, _ := hostFixture(t, true)
	body := getPage(srv)
	compose, host := rowOf(t, body, "db"), rowOf(t, body, "host:db")
	for _, w := range []string{`data-action="service-stop"`, `>host<`, "Running", `>db</button>`} {
		if !strings.Contains(host, w) {
			t.Errorf("host row lacks %q:\n%s", w, host)
		}
	}
	if strings.Contains(host, "host:db</button>") || strings.Contains(compose, ">host<") {
		t.Errorf("label or image leaked between rows")
	}
}

func TestHostRowActionsRouteByPrefix(t *testing.T) {
	installFakeDocker(t)
	srv, project, dir := hostFixture(t, true)
	log := filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("DOCKER_FAKE_LOG", log)
	var restarted string
	old := lifecycle.HostRestart
	lifecycle.HostRestart = func(_ io.Writer, _, _, name, _, _ string) error { restarted = name; return nil }
	t.Cleanup(func() { lifecycle.HostRestart = old })

	// plain "db" is compose: docker is called, the host entry is untouched
	if code, resp := postAction(t, srv, "127.0.0.1:55903", actionRequestFixture{Action: "service-stop", Project: project, Service: "db"}); code != 200 || !resp.OK {
		t.Fatalf("compose stop: %d %+v", code, resp)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "stop db") {
		t.Errorf("compose stop did not reach docker: %q", b)
	}
	if m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json")); len(m.HostProcs) != 1 {
		t.Errorf("compose stop touched the host process")
	}

	// restart and start on host:db go to the host hook, with the bare name
	for _, a := range []string{"restart", "service-start"} {
		restarted = ""
		if code, resp := postAction(t, srv, "127.0.0.1:55903", actionRequestFixture{Action: a, Project: project, Service: "host:db"}); code != 200 || !resp.OK || restarted != "db" {
			t.Errorf("%s: %d %+v restarted=%q", a, code, resp, restarted)
		}
	}
}

func TestHostStopOnStaleEntryKeepsTheRowWithoutDocker(t *testing.T) {
	installFakeDocker(t)
	srv, project, dir := hostFixture(t, false)
	log := filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("DOCKER_FAKE_LOG", log)
	// a stale entry shows as an exited row with a Start item
	row := rowOf(t, getPage(srv), "host:db")
	if !strings.Contains(row, "Stopped") || !strings.Contains(row, `data-action="service-start"`) {
		t.Errorf("stale host row:\n%s", row)
	}
	os.Remove(log)
	if code, resp := postAction(t, srv, "127.0.0.1:55903", actionRequestFixture{Action: "service-stop", Project: project, Service: "host:db"}); code != 200 || !resp.OK {
		t.Fatalf("host stop: %d %+v", code, resp)
	}
	if m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json")); len(m.HostProcs) != 1 {
		t.Errorf("the stopped row must stay, with Start: %+v", m.HostProcs)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "stop") {
		t.Errorf("host stop reached docker: %q", b)
	}
}

func TestLogsForHostServiceReadsFile(t *testing.T) {
	installFakeDocker(t)
	srv, project, dir := hostFixture(t, false)
	t.Setenv("DOCKER_FAKE_LOGS", "DOCKER-LOGS\n")
	if err := os.WriteFile(filepath.Join(dir, "host-db.log"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := getLogs(t, srv, "127.0.0.1:55903", project, "service=host:db&tail=2")
	if rec.Code != 200 || rec.Body.String() != "two\nthree\n" {
		t.Fatalf("host logs: %d %q", rec.Code, rec.Body.String())
	}
	if rec = getLogs(t, srv, "127.0.0.1:55903", project, "service=db"); !strings.Contains(rec.Body.String(), "DOCKER-LOGS") {
		t.Errorf("plain name must read docker logs, got %q", rec.Body.String())
	}
	rec = getLogs(t, srv, "127.0.0.1:55903", project, "service=host:db&tail=1&follow=1")
	if want := "three\n(live follow is not available for host apps; reopen to refresh)\n"; rec.Body.String() != want {
		t.Errorf("follow: %q", rec.Body.String())
	}
}

func TestHostLogsReadOnlyTheEndOfABigFile(t *testing.T) {
	installFakeDocker(t)
	srv, project, dir := hostFixture(t, false)
	big := strings.Repeat("filler line of text\n", 40000) + "last-1\nlast-2\n" // ~800 KiB
	if err := os.WriteFile(filepath.Join(dir, "host-db.log"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := getLogs(t, srv, "127.0.0.1:55903", project, "service=host:db&tail=100000")
	out := rec.Body.String()
	if !strings.HasSuffix(out, "last-1\nlast-2\n") || len(out) > 256<<10 || len(out) < 200<<10 {
		t.Fatalf("len %d, tail %q", len(out), out[max(0, len(out)-30):])
	}
	if !strings.HasPrefix(out, "filler line of text\n") {
		t.Errorf("partial first line not dropped: %q", out[:30])
	}
}
