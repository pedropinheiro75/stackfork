package dashboard_test

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestServicePlayStopButtonMatchesState covers fix 5: a stopped service
// inside a partial stack gets a per-service play button (service-start),
// and a running one gets a stop button (service-stop) - the fix's whole
// point being that restart alone isn't a way to start a stopped service
// sitting next to a running one.
func TestServicePlayStopButtonMatchesState(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")

	project := "sf-svcplaystop-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{"services": map[string]interface{}{
		"web": map[string]interface{}{}, "db": map[string]interface{}{},
	}}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}
	psLine := func(service, state string) string {
		labels := "com.docker.compose.project=" + project + ",com.docker.compose.service=" + service + ",io.stackfork.root=/x"
		row := map[string]string{"Labels": labels, "State": state, "Status": "Up", "Ports": ""}
		b, _ := json.Marshal(row)
		return string(b)
	}
	t.Setenv("DOCKER_FAKE_PS", psLine("web", "running")+"\n"+psLine("db", "exited")+"\n")

	srv, err := dashboard.New(newBase, legacyBase, "test", 55902)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55902"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	serviceSection := func(service string) string {
		start := strings.Index(body, `class="service-row" data-service="`+service+`"`)
		if start < 0 {
			t.Fatalf("service %s not found", service)
		}
		end := strings.Index(body[start:], `class="service-settings"`) + start
		if end < start {
			t.Fatalf("service-settings marker not found after %s", service)
		}
		return body[start:end]
	}

	web := serviceSection("web")
	if !strings.Contains(web, `data-action="service-stop"`) {
		t.Errorf("running web service: expected a service-stop button, got:\n%s", web)
	}
	if strings.Contains(web, `data-action="service-start"`) {
		t.Errorf("running web service: did not expect a service-start button, got:\n%s", web)
	}

	db := serviceSection("db")
	if !strings.Contains(db, `data-action="service-start"`) {
		t.Errorf("stopped db service: expected a service-start button, got:\n%s", db)
	}
	if strings.Contains(db, `data-action="service-stop"`) {
		t.Errorf("stopped db service: did not expect a service-stop button, got:\n%s", db)
	}
}

func TestHostServiceRowActionsAndLogs(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")
	project := "sf-hostrow-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	start, err := state.ProcStart(os.Getpid())
	if err != nil {
		t.Skip("no process start time on this platform")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
		HostProcs: map[string]state.HostProc{"app": {PID: os.Getpid(), StartedAt: start, Cmdline: os.Args, Port: ln.Addr().(*net.TCPAddr).Port}},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), map[string]interface{}{"services": map[string]interface{}{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host-app.log"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := dashboard.New(newBase, legacyBase, "test", 55903)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55903"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	i := strings.Index(body, `class="service-row" data-service="app"`)
	if i < 0 {
		t.Fatalf("host row missing:\n%s", body)
	}
	row := body[i : i+strings.Index(body[i:], `class="service-settings"`)]
	for _, w := range []string{`data-action="service-stop"`, `data-action="restart"`, `>host<`, "Running"} {
		if !strings.Contains(row, w) {
			t.Errorf("host row lacks %q:\n%s", w, row)
		}
	}
}

// TestLogsForHostServiceReadsFile: a service that is a host process reads
// its host-NAME.log, last lines only, and never calls docker.
func TestLogsForHostServiceReadsFile(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	t.Setenv("DOCKER_FAKE_LOGS", "DOCKER-WAS-CALLED\n")
	dir := filepath.Join(newBase, project)
	mp := filepath.Join(dir, "manifest.json")
	m, err := state.ReadManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	m.HostProcs = map[string]state.HostProc{"app": {PID: 999999, StartedAt: "x", Cmdline: []string{"x"}, Port: 40998}}
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host-app.log"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, err := dashboard.New(newBase, legacyBase, "test", 55904)
	if err != nil {
		t.Fatal(err)
	}
	rec := getLogs(t, srv, "127.0.0.1:55904", project, "service=app&host=1&tail=2")
	if rec.Code != 200 || rec.Body.String() != "two\nthree\n" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}
