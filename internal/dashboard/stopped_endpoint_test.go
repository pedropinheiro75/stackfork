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

// TestStoppedServiceWithPublishedPortStillShowsLink covers the maintainer's
// feedback on task 3.6: a service with no running container must still show
// every port declared in its snapshot - an HTTP port as a (greyed, "stopped"
// noted) clickable link, not silently dropped just because nothing is
// running to report a live docker ps row for it.
func TestStoppedServiceWithPublishedPortStillShowsLink(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")

	project := "sf-stoppedhttp-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
		HTTPPorts: []int{3000},
		Ports:     []state.PortAlloc{{Service: "web", Target: 3000, Host: 20099}},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{
		"services": map[string]interface{}{
			"web": map[string]interface{}{
				"image": "node:22",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("3000"), "published": "20099", "protocol": "tcp"},
				},
			},
		},
	}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}
	// No docker ps row for this project at all: the service has never been
	// started (or was stopped), same as $DOCKER_FAKE_PS being empty.

	srv, err := dashboard.New(newBase, legacyBase, "test", 55601)
	if err != nil {
		t.Fatal(err)
	}

	// The API must carry the link too, not just the HTML.
	reqAPI := httptest.NewRequest("GET", "/api/instances", nil)
	reqAPI.Host = "127.0.0.1:55601"
	recAPI := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recAPI, reqAPI)
	if recAPI.Code != 200 {
		t.Fatalf("GET /api/instances: status %d, body %s", recAPI.Code, recAPI.Body.String())
	}
	var inv struct {
		Instances []struct {
			Project  string `json:"project"`
			State    string `json:"state"`
			Services []struct {
				Name      string `json:"name"`
				State     string `json:"state"`
				Endpoints []struct {
					Scheme string `json:"scheme"`
					URL    string `json:"url"`
					Known  bool   `json:"known"`
				} `json:"endpoints"`
			} `json:"services"`
		} `json:"instances"`
	}
	if err := json.Unmarshal(recAPI.Body.Bytes(), &inv); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, recAPI.Body.String())
	}
	var found bool
	for _, i := range inv.Instances {
		if i.Project != project {
			continue
		}
		if i.State != "stopped" {
			t.Fatalf("expected instance state stopped, got %q", i.State)
		}
		for _, s := range i.Services {
			if s.Name != "web" {
				continue
			}
			if s.State != "stopped" {
				t.Fatalf("expected service state stopped, got %q", s.State)
			}
			if len(s.Endpoints) != 1 || !s.Endpoints[0].Known || s.Endpoints[0].Scheme != "http" {
				t.Fatalf("expected one known http endpoint for a stopped service, got %+v", s.Endpoints)
			}
			if s.Endpoints[0].URL != "http://"+project+".localhost:20099" {
				t.Fatalf("unexpected endpoint URL %q", s.Endpoints[0].URL)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("web service not found in /api/instances: %+v", inv)
	}

	// The rendered page must show it as a clickable link, greyed/noted as
	// stopped, not silently omitted.
	reqPage := httptest.NewRequest("GET", "/", nil)
	reqPage.Host = "127.0.0.1:55601"
	recPage := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recPage, reqPage)
	body := recPage.Body.String()
	if !strings.Contains(body, `href="http://`+project+`.localhost:20099"`) {
		t.Fatalf("expected a clickable link for the stopped service's HTTP port:\n%s", body)
	}
	if !strings.Contains(body, `class="endpoint-link stopped"`) {
		t.Fatalf("expected the link greyed via the \"stopped\" class:\n%s", body)
	}
	if !strings.Contains(body, "(stopped)") {
		t.Fatalf("expected a \"(stopped)\" note on the link:\n%s", body)
	}
}

// TestStoppedRandomPortServiceShowsAssignedOnStart covers the other half
// of the maintainer's feedback: a legacy/--random-ports service that isn't
// running has no way to know its actual port (Docker assigns it fresh on
// every start), so it shows the container port and "assigned on start"
// instead of a stale or fabricated address.
func TestStoppedRandomPortServiceShowsAssignedOnStart(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")

	project := "sf-randomport-1"
	dir := filepath.Join(newBase, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// No Ports allocation at all: a --random-ports/legacy instance.
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	config := map[string]interface{}{
		"services": map[string]interface{}{
			"db": map[string]interface{}{
				"image": "postgres:17",
				"ports": []interface{}{
					map[string]interface{}{"target": json.Number("5432"), "protocol": "tcp"},
				},
			},
		},
	}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}

	srv, err := dashboard.New(newBase, legacyBase, "test", 55602)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Host = "127.0.0.1:55602"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /api/instances: status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"known":false`) {
		t.Fatalf("expected an unknown-port endpoint, got:\n%s", body)
	}

	reqPage := httptest.NewRequest("GET", "/", nil)
	reqPage.Host = "127.0.0.1:55602"
	recPage := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recPage, reqPage)
	if !strings.Contains(recPage.Body.String(), "assigned on start") {
		t.Fatalf("expected \"assigned on start\" for the unresolved port:\n%s", recPage.Body.String())
	}
}
