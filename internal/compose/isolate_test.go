package compose

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// Ported from the original Python CLI's check(): isolate() safety and refusals.
func TestIsolateRewritesAndScopesNames(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	source, err := DecodeJSON([]byte(`{
		"services": {"db": {
			"image": "postgres:17", "container_name": "shared-db",
			"ports": [{"target": 5432, "published": "5432", "host_ip": "0.0.0.0"}],
			"volumes": [{"type": "volume", "source": "data", "target": "/var/lib/postgresql/data"}],
			"healthcheck": {"test": ["CMD-SHELL", "echo $$POSTGRES_USER"]}
		}},
		"volumes": {"data": {"name": "shared-data"}},
		"networks": {"default": {"name": "shared-network"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}

	ma, err := Isolate(source, a, "sf-a-000", "")
	if err != nil {
		t.Fatal(err)
	}
	mb, err := Isolate(source, b, "sf-b-000", "")
	if err != nil {
		t.Fatal(err)
	}
	modelA, modelB := ma.(map[string]interface{}), mb.(map[string]interface{})

	volA := modelA["volumes"].(map[string]interface{})["data"].(map[string]interface{})["name"]
	volB := modelB["volumes"].(map[string]interface{})["data"].(map[string]interface{})["name"]
	if volA == volB {
		t.Fatalf("expected distinct volume names, both %v", volA)
	}
	netA := modelA["networks"].(map[string]interface{})["default"].(map[string]interface{})["name"]
	netB := modelB["networks"].(map[string]interface{})["default"].(map[string]interface{})["name"]
	if netA == netB {
		t.Fatalf("expected distinct network names, both %v", netA)
	}

	dbA := modelA["services"].(map[string]interface{})["db"].(map[string]interface{})
	if _, has := dbA["container_name"]; has {
		t.Fatal("container_name must be stripped")
	}
	port := dbA["ports"].([]interface{})[0].(map[string]interface{})
	if port["host_ip"] != "127.0.0.1" {
		t.Fatalf("expected host_ip 127.0.0.1, got %v", port["host_ip"])
	}
	if _, has := port["published"]; has {
		t.Fatal("published must be stripped so Docker assigns a random host port")
	}

	// The source must never be mutated (Python's copy.deepcopy equivalent).
	srcMap := source.(map[string]interface{})
	srcPort := srcMap["services"].(map[string]interface{})["db"].(map[string]interface{})["ports"].([]interface{})[0].(map[string]interface{})
	if srcPort["published"].(string) != "5432" {
		t.Fatalf("mutated input: published = %v", srcPort["published"])
	}
}

func TestIsolateRefusals(t *testing.T) {
	a := t.TempDir()
	bad := []string{
		`{"volumes": {"data": {"external": true}}}`,
		`{"networks": {"default": {"external": true}}}`,
		`{"networks": {"default": {"ipam": {"config": [{"subnet": "172.20.0.0/16"}]}}}}`,
		`{"services": {"db": {"image": "postgres:17", "network_mode": "host"}}}`,
		`{"services": {"db": {"image": "postgres:17", "privileged": true}}}`,
		`{"services": {"db": {"image": "postgres:17", "env_file": [{"path": "` + filepath.Join(a, ".env") + `"}]}}}`,
		`{"services": {"db": {"image": "postgres:17", "volumes": [{"type": "bind", "source": "/local_db", "target": "/var/lib/postgresql/data"}]}}}`,
	}
	for _, raw := range bad {
		model, err := DecodeJSON([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Isolate(model, a, "sf-a-000", ""); err == nil {
			t.Fatalf("accepted unsafe config: %s", raw)
		}
	}
}

// Task 13.3: collect-all mode reports every refusal and rewrite instead of
// stopping at the first, each with a reason.
func TestIsolateDryRunCollectsAllFindings(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // a writable bind outside the checkout, e.g. /local_db
	source, err := DecodeJSON([]byte(`{
		"services": {"db": {
			"image": "postgres:17",
			"container_name": "fixed-db",
			"volumes": [
				{"type": "bind", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock"},
				{"type": "bind", "source": "` + outside + `", "target": "/data"}
			]
		}},
		"networks": {"default": {"external": true}}
	}`))
	if err != nil {
		t.Fatal(err)
	}

	findings, err := IsolateDryRun(source, root, "sf-a-000", "")
	if err != nil {
		t.Fatalf("unexpected structural error: %v", err)
	}

	want := map[string]string{
		"container_name":                 actionRewrite,
		"external_resource":              actionRefuse,
		"socket_mount":                   actionRefuse,
		"writable_bind_outside_checkout": actionRefuse,
	}
	got := map[string]string{}
	for _, f := range findings {
		if f.Reason == "" {
			t.Fatalf("finding %+v has no reason", f)
		}
		got[f.Kind] = f.Action
	}
	for kind, action := range want {
		if got[kind] != action {
			t.Fatalf("expected a %q finding with action %q, got findings: %+v", kind, action, findings)
		}
	}
}

func TestRegistryToken(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, ".npmrc")
	if err := os.WriteFile(registry, []byte("//npm.pkg.github.com/:_authToken=\"synthetic-test-token\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := RegistryToken(registry)
	if err != nil {
		t.Fatal(err)
	}
	if token != "synthetic-test-token" {
		t.Fatalf("got %q", token)
	}

	if err := os.WriteFile(registry, []byte("//npm.pkg.github.com/:_authToken=${RR_TEST_MISSING_TOKEN}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("RR_TEST_MISSING_TOKEN")
	if _, err := RegistryToken(registry); err == nil {
		t.Fatal("accepted unresolved registry credentials")
	}
}

func TestPublicStatusAndEndpointLines(t *testing.T) {
	rows, err := PublicStatus(`{"ID": "fixture", "Service": "nginx", "Command": "private arguments", "Publishers": [{"TargetPort": 80, "PublishedPort": 0}, {"TargetPort": 3000, "PublishedPort": 49152}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if len(rows[0].Publishers) != 1 {
		t.Fatalf("expected the unpublished port dropped, got %v", rows[0].Publishers)
	}
	lines := EndpointLines(rows, "eup-274.localhost", nil, nil)
	want := "nginx: http://eup-274.localhost:49152 (3000/tcp)"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v want [%s]", lines, want)
	}

	dbRow, err := PublicStatus(`{"Service": "db", "Publishers": [{"TargetPort": 5432, "PublishedPort": 49153}]}`)
	if err != nil {
		t.Fatal(err)
	}
	lines = EndpointLines(dbRow, "eup-274.localhost", nil, nil)
	want = "db: tcp://127.0.0.1:49153 (5432/tcp)"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v want [%s]", lines, want)
	}
}

func hasDocker(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "compose", "version").Run() == nil
}

// Ported from the original Python CLI's check(): a real `docker compose
// config` round trip, including literal $ handling.
func TestRenderIsolateRoundTrip(t *testing.T) {
	if !hasDocker(t) {
		t.Skip("docker compose not available")
	}
	dir := t.TempDir()
	env := filepath.Join(dir, "runtime.env")
	if err := os.WriteFile(env, []byte("VALUE=test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "source.json")
	source := `{
		"services": {"db": {
			"image": "postgres:17",
			"environment": {"LITERAL": "price$$100", "POSTGRES_USER": "postgres"},
			"env_file": [{"path": "` + env + `"}],
			"healthcheck": {"test": ["CMD-SHELL", "echo $$POSTGRES_USER"]}
		}}
	}`
	if err := os.WriteFile(config, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	rendered, _, err := Render(dir, []string{config}, env, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	model, err := Isolate(rendered, dir, "sf-a-000", env)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "isolated.json")
	b, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, b, 0o600); err != nil {
		t.Fatal(err)
	}

	roundtrip, _, err := Render(dir, []string{output}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	rt := roundtrip.(map[string]interface{})
	rendMap := rendered.(map[string]interface{})
	rtEnv := rt["services"].(map[string]interface{})["db"].(map[string]interface{})["environment"].(map[string]interface{})
	rendEnv := rendMap["services"].(map[string]interface{})["db"].(map[string]interface{})["environment"].(map[string]interface{})
	if rtEnv["LITERAL"] != rendEnv["LITERAL"] {
		t.Fatalf("literal $ mishandled: %v != %v", rtEnv["LITERAL"], rendEnv["LITERAL"])
	}
	rtHealth := rt["services"].(map[string]interface{})["db"].(map[string]interface{})["healthcheck"].(map[string]interface{})["test"]
	rendHealth := rendMap["services"].(map[string]interface{})["db"].(map[string]interface{})["healthcheck"].(map[string]interface{})["test"]
	if !equalJSON(rtHealth, rendHealth) {
		t.Fatalf("healthcheck mishandled: %v != %v", rtHealth, rendHealth)
	}
	rtPort := rt["services"].(map[string]interface{})["db"].(map[string]interface{})["ports"]
	if rtPort != nil {
		ports := rtPort.([]interface{})
		if len(ports) > 0 {
			if pub, has := ports[0].(map[string]interface{})["published"]; has && pub != "0" && pub != nil {
				t.Fatalf("expected no re-published port after round trip, got %v", pub)
			}
		}
	}
}

func equalJSON(a, b interface{}) bool {
	ba, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return strings.TrimSpace(string(ba)) == strings.TrimSpace(string(bb))
}

// Task 6.0: WireSecrets must declare the secret at the top level and grant
// it only to a service that already references it by name, when one does.
func TestWireSecretsGrantsExplicitConsumerOnly(t *testing.T) {
	source, err := DecodeJSON([]byte(`{
		"services": {
			"app": {"build": {"context": "."}},
			"worker": {"image": "x", "secrets": ["npm_token"]}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	model, err := Isolate(source, t.TempDir(), "sf-a-000", "")
	if err != nil {
		t.Fatal(err)
	}
	WireSecrets(model, []string{"npm_token"})

	m := model.(map[string]interface{})
	secrets, ok := m["secrets"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a top-level secrets section, got %v", m["secrets"])
	}
	entry, ok := secrets["npm_token"].(map[string]interface{})
	if !ok || entry["environment"] != "npm_token" {
		t.Fatalf("expected npm_token: {environment: npm_token}, got %v", secrets["npm_token"])
	}

	services := m["services"].(map[string]interface{})
	if _, has := services["app"].(map[string]interface{})["secrets"]; has {
		t.Fatalf("app has no explicit reference and worker does; app must not be granted: %v", services["app"])
	}
	workerSecrets, _ := services["worker"].(map[string]interface{})["secrets"].([]interface{})
	if len(workerSecrets) != 1 || workerSecrets[0] != "npm_token" {
		t.Fatalf("expected worker to keep its own npm_token reference, got %v", workerSecrets)
	}
}

// Task 6.0: with no explicit consumer, every service with a `build` is
// granted the secret - this is how the template's `app` service (whose
// nuxt.compose.yaml declares no `secrets:` of its own) gets npm_token.
func TestWireSecretsFallsBackToBuildServices(t *testing.T) {
	source, err := DecodeJSON([]byte(`{
		"services": {
			"app": {"build": {"context": "."}},
			"db": {"image": "postgres:17"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	model, err := Isolate(source, t.TempDir(), "sf-a-000", "")
	if err != nil {
		t.Fatal(err)
	}
	WireSecrets(model, []string{"npm_token"})

	services := model.(map[string]interface{})["services"].(map[string]interface{})
	appSecrets, _ := services["app"].(map[string]interface{})["secrets"].([]interface{})
	if len(appSecrets) != 1 || appSecrets[0] != "npm_token" {
		t.Fatalf("expected the build service granted npm_token, got %v", appSecrets)
	}
	if _, has := services["db"].(map[string]interface{})["secrets"]; has {
		t.Fatalf("db has no build and is not an explicit consumer; must not be granted: %v", services["db"])
	}
}

func TestWireSecretsNoNamesIsNoop(t *testing.T) {
	source, err := DecodeJSON([]byte(`{"services": {"app": {"build": {"context": "."}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	model, err := Isolate(source, t.TempDir(), "sf-a-000", "")
	if err != nil {
		t.Fatal(err)
	}
	WireSecrets(model, nil)
	m := model.(map[string]interface{})
	if _, has := m["secrets"]; has {
		t.Fatalf("expected no secrets section with no names, got %v", m["secrets"])
	}
}

// Task 7.1: pins docker compose config --format json's behaviour with a
// service-level x-* extension key, on the installed Compose version - it's
// dropped, which is why HTTP marking uses the io.stackfork.http/.https
// labels instead (identity.LabelHTTP, read back by DeclaredHTTPPorts). If a
// future Compose version changes this, this test catches it rather than the
// label silently going unused.
func TestComposeConfigDropsServiceLevelExtensionKeys(t *testing.T) {
	if !hasDocker(t) {
		t.Skip("docker compose not available")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "source.json")
	source := `{
		"services": {"app": {
			"image": "nginx:alpine",
			"x-stackfork": {"http": [5173]}
		}}
	}`
	if err := os.WriteFile(config, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, _, err := Render(dir, []string{config}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := rendered.(map[string]interface{})
	services, _ := m["services"].(map[string]interface{})
	app, _ := services["app"].(map[string]interface{})
	if _, has := app["x-stackfork"]; has {
		t.Fatal("docker compose config now keeps service-level x-* keys; update this pin and this comment to match")
	}
}

// Task 7.2: a service that declares target port 5173 as HTTP (via
// io.stackfork.http, since x-stackfork doesn't survive compose config per
// task 7.1) gets an http:// URL instead of falling through to tcp://.
func TestDeclaredHTTPPortUsesHTTPScheme(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {
			"web": {"labels": {"` + identity.LabelHTTP + `": "5173"}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	httpPorts, httpsPorts := DeclaredHTTPPorts(model)
	if len(httpPorts) != 1 || httpPorts[0] != 5173 || len(httpsPorts) != 0 {
		t.Fatalf("got http=%v https=%v", httpPorts, httpsPorts)
	}

	rows, err := PublicStatus(`{"Service": "web", "Publishers": [{"TargetPort": 5173, "PublishedPort": 49200, "Protocol": "tcp"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	lines := EndpointLines(rows, "eup-274.localhost", httpPorts, httpsPorts)
	want := "web: http://eup-274.localhost:49200 (5173/tcp)"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v want [%s]", lines, want)
	}
}

// A declared HTTPS port wins over the fixed-port fallback guess.
func TestDeclaredHTTPSPortWinsOverFixedList(t *testing.T) {
	// 3000 is in the fixed http list; declaring it https must override that.
	rows, err := PublicStatus(`{"Service": "app", "Publishers": [{"TargetPort": 3000, "PublishedPort": 49201, "Protocol": "tcp"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	lines := EndpointLines(rows, "eup-274.localhost", nil, []int{3000})
	want := "app: https://eup-274.localhost:49201 (3000/tcp)"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v want [%s]", lines, want)
	}
}

// Task 9.2: io.stackfork.primary=true wins even when a lower-sorted service
// also declares an HTTP port.
func TestPrimaryHTTPServiceExplicitLabelWins(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {
			"admin": {"labels": {"` + identity.LabelHTTP + `": "9000"}},
			"web": {"labels": {"` + identity.LabelHTTP + `": "3000", "` + identity.LabelPrimary + `": "true"}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	service, target, https, ok := PrimaryHTTPService(model)
	if !ok || service != "web" || target != 3000 || https {
		t.Fatalf("got service=%s target=%d https=%v ok=%v", service, target, https, ok)
	}
}

// Task 9.2: with no io.stackfork.primary label, the first declared HTTP
// service by name wins.
func TestPrimaryHTTPServiceFirstDeclared(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {
			"web": {"labels": {"` + identity.LabelHTTPS + `": "8443"}},
			"admin": {"labels": {"` + identity.LabelHTTP + `": "9000"}}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	service, target, https, ok := PrimaryHTTPService(model)
	if !ok || service != "admin" || target != 9000 || https {
		t.Fatalf("got service=%s target=%d https=%v ok=%v", service, target, https, ok)
	}
}

// Task 9.2: with no declared label at all, fall back to the same
// fixed-port guess EndpointLines uses.
func TestPrimaryHTTPServiceInferredFallback(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {
			"db": {"ports": [{"target": 5432}]},
			"web": {"ports": [{"target": 3000}]}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	service, target, https, ok := PrimaryHTTPService(model)
	if !ok || service != "web" || target != 3000 || https {
		t.Fatalf("got service=%s target=%d https=%v ok=%v", service, target, https, ok)
	}
}

// TestPrimaryHTTPServiceGuessPriorityBeatsAlphabeticalOrder is the real
// bug behind app-d's dashboard "App" chip opening Adminer instead of the
// app: "adminer" (port 8080) sorts before "app" (port 3000) alphabetically,
// but 3000 ranks earlier in the fixed-port guess list (80/443/3000/8080/
// 5050) - guess priority must win over which service name comes first.
func TestPrimaryHTTPServiceGuessPriorityBeatsAlphabeticalOrder(t *testing.T) {
	model, err := DecodeJSON([]byte(`{
		"services": {
			"adminer": {"ports": [{"target": 8080}]},
			"app": {"ports": [{"target": 3000}]}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	service, target, https, ok := PrimaryHTTPService(model)
	if !ok || service != "app" || target != 3000 || https {
		t.Fatalf("got service=%s target=%d https=%v ok=%v, want app/3000", service, target, https, ok)
	}
}

func TestPrimaryHTTPServiceNoneQualifies(t *testing.T) {
	model, err := DecodeJSON([]byte(`{"services": {"db": {"ports": [{"target": 5432}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := PrimaryHTTPService(model); ok {
		t.Fatal("expected no primary HTTP service for a bare database port")
	}
}

// Task 9.2: PublishedHostPort re-resolves a service's currently published
// host port from `docker compose ps` rows, the way a random-port/legacy
// instance's route is re-resolved on every proxy reload.
func TestPublishedHostPort(t *testing.T) {
	rows, err := PublicStatus(`{"Service": "web", "Publishers": [{"TargetPort": 3000, "PublishedPort": 51234, "Protocol": "tcp"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	port, ok := PublishedHostPort(rows, "web", 3000)
	if !ok || port != 51234 {
		t.Fatalf("got port=%d ok=%v", port, ok)
	}
	if _, ok := PublishedHostPort(rows, "web", 9999); ok {
		t.Fatal("expected no match for an unpublished target port")
	}
}

// Task 9.4: PrimaryLine is portless on the default proxy port, and appends
// :port for a non-default one.
func TestPrimaryLine(t *testing.T) {
	if got := PrimaryLine("web", "eup-274.localhost", false, 80); got != "web: http://eup-274.localhost" {
		t.Fatalf("got %q", got)
	}
	if got := PrimaryLine("web", "eup-274.localhost", true, 8443); got != "web: https://eup-274.localhost:8443" {
		t.Fatalf("got %q", got)
	}
}
