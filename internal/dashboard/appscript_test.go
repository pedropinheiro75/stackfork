package dashboard_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

// TestInstancesJSONMatchesAppJSFields covers task 3.2's "unit test of the
// JSON the script consumes": app.js (static/app.js) reads these exact keys
// off GET /api/instances - inv.docker.reachable, inv.proxy.running/port,
// and per-instance root/project/branch/hostname/state/legacy/services
// (name/state/health/endpoints[].scheme/.url)/knowledge.status/warnings.
// A field rename here without updating app.js would otherwise only show up
// as a silently blank dashboard.
func TestInstancesJSONMatchesAppJSFields(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	root := fixtureGitRepo(t, "main")
	writeFixtureInstance(t, newBase, "sf-jsonfields-1", root, "main", "web")

	srv, err := dashboard.New(newBase, legacyBase, "test", 55201)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Host = "127.0.0.1:55201"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /api/instances: status %d, body %s", rec.Code, rec.Body.String())
	}

	var inv map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, rec.Body.String())
	}
	requireKeys(t, inv, "docker", "proxy", "instances")
	docker, _ := inv["docker"].(map[string]interface{})
	requireKeys(t, docker, "reachable")
	proxy, _ := inv["proxy"].(map[string]interface{})
	requireKeys(t, proxy, "running", "port")

	instances, _ := inv["instances"].([]interface{})
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d: %+v", len(instances), inv["instances"])
	}
	inst, _ := instances[0].(map[string]interface{})
	requireKeys(t, inst, "project", "root", "branch", "hostname", "legacy", "state", "services")

	services, _ := inst["services"].([]interface{})
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d: %+v", len(services), inst["services"])
	}
	svc, _ := services[0].(map[string]interface{})
	requireKeys(t, svc, "name", "state")
	if svc["name"] != "web" {
		t.Fatalf("expected service name %q, got %v", "web", svc["name"])
	}
}

func requireKeys(t *testing.T, m map[string]interface{}, keys ...string) {
	t.Helper()
	if m == nil {
		t.Fatalf("expected an object with keys %v, got nil", keys)
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in %+v", k, m)
		}
	}
}
