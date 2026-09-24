package compose

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	lines := EndpointLines(rows, "eup-274.localhost")
	want := "nginx: http://eup-274.localhost:49152 (3000/tcp)"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %v want [%s]", lines, want)
	}

	dbRow, err := PublicStatus(`{"Service": "db", "Publishers": [{"TargetPort": 5432, "PublishedPort": 49153}]}`)
	if err != nil {
		t.Fatal(err)
	}
	lines = EndpointLines(dbRow, "eup-274.localhost")
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

	rendered, err := Render(dir, []string{config}, env, "")
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

	roundtrip, err := Render(dir, []string{output}, "", "")
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
