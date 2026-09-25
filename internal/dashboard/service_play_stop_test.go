package dashboard_test

import (
	"encoding/json"
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
