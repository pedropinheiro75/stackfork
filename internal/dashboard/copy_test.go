package dashboard_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// copyFixture writes one instance's manifest.json + compose.json (as
// prepare would, matching the source compose.yaml's Files entry) so
// POST /api/copy's password resolution has real source files to scan, not
// just the rendered snapshot.
func copyFixture(t *testing.T, base, project, sourceYAML, config string) (root string) {
	t.Helper()
	root = fixtureGitRepo(t, "main")
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(sourceYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: "main", Hostname: project + ".localhost",
		Files: []string{composePath}, Inputs: map[string]string{},
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func postCopy(t *testing.T, srv *dashboard.Server, host string, headers map[string]string, body map[string]string) (int, map[string]string) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/copy", bytes.NewReader(b))
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp) //nolint:errcheck // resp stays nil/empty on a non-JSON error body, which the caller's assertions cover
	return rec.Code, resp
}

const copyFixtureConfig = `{
  "services": {
    "db": {
      "image": "postgres:17",
      "ports": [{"target": 5432, "published": "20200", "protocol": "tcp"}],
      "environment": {"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb", "POSTGRES_PASSWORD": "literal-canary-pw"}
    }
  }
}`

// --- negative cases (D5 boundary): written first, same as LiteralPassword's
// own negative tests - the property that matters most is what this
// endpoint refuses, not what it returns.

func TestCopyRejectsWithoutTokenOrOrigin(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	copyFixture(t, newBase, "sf-copyauth-1", copyFixtureConfig, copyFixtureConfig)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55970)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55970"
	body := map[string]string{"project": "sf-copyauth-1", "service": "db", "kind": "database_url"}

	code, _ := postCopy(t, srv, host, nil, body)
	if code == 200 {
		t.Errorf("missing token: status %d, want rejection", code)
	}

	code, _ = postCopy(t, srv, host, map[string]string{
		"X-Stackfork-Token": "wrong-token", "Origin": "http://" + host,
	}, body)
	if code == 200 {
		t.Errorf("wrong token: status %d, want rejection", code)
	}

	code, _ = postCopy(t, srv, host, map[string]string{
		"X-Stackfork-Token": srv.Token, "Origin": "http://evil.example.com",
	}, body)
	if code == 200 {
		t.Errorf("foreign origin: status %d, want rejection", code)
	}

	// GET must not be routed at all (mux only registers POST /api/copy).
	req := httptest.NewRequest("GET", "/api/copy", nil)
	req.Host = host
	req.Header.Set("X-Stackfork-Token", srv.Token)
	req.Header.Set("Origin", "http://"+host)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == 200 {
		t.Errorf("GET /api/copy: status %d, want rejection", rec.Code)
	}
}

// TestCopySecretBackedPasswordNeverReturned covers D3b's core guarantee: a
// service whose password comes through an env_file gets the placeholder
// back, not the value the (attacker-controlled, in a real env_file) string
// might contain - the request still succeeds (200), it just never leaks.
func TestCopySecretBackedPasswordNeverReturned(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	const canary = "canary-envfile-password-should-never-leak"
	sourceYAML := `
services:
  db:
    image: postgres:17
    env_file:
      - .env.db
    environment:
      POSTGRES_USER: appuser
`
	config := `{
  "services": {
    "db": {
      "image": "postgres:17",
      "ports": [{"target": 5432, "published": "20201", "protocol": "tcp"}],
      "environment": {"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "` + canary + `"}
    }
  }
}`
	copyFixture(t, newBase, "sf-copyenvfile-1", sourceYAML, config)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55971)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55971"
	code, resp := postCopy(t, srv, host, map[string]string{
		"X-Stackfork-Token": srv.Token, "Origin": "http://" + host,
	}, map[string]string{"project": "sf-copyenvfile-1", "service": "db", "kind": "database_url"})
	if code != 200 {
		t.Fatalf("status %d, body %+v", code, resp)
	}
	if strings.Contains(resp["value"], canary) {
		t.Fatalf("env_file-backed password leaked: %q", resp["value"])
	}
	if !strings.Contains(resp["value"], "<password>") {
		t.Fatalf("expected the placeholder to remain, got %q", resp["value"])
	}
}

// TestCopyInterpolatedPasswordNeverReturned covers the ${VAR} case
// (design.md D3b: "${VAR} interpolation" is never included), including
// when VAR would have come from a --secret.
func TestCopyInterpolatedPasswordNeverReturned(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	sourceYAML := `
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: appuser
      POSTGRES_PASSWORD: ${npm_token}
`
	config := `{
  "services": {
    "db": {
      "image": "postgres:17",
      "ports": [{"target": 5432, "published": "20202", "protocol": "tcp"}],
      "environment": {"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "${npm_token}"}
    }
  }
}`
	copyFixture(t, newBase, "sf-copyinterp-1", sourceYAML, config)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55972)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55972"
	code, resp := postCopy(t, srv, host, map[string]string{
		"X-Stackfork-Token": srv.Token, "Origin": "http://" + host,
	}, map[string]string{"project": "sf-copyinterp-1", "service": "db", "kind": "database_url"})
	if code != 200 {
		t.Fatalf("status %d, body %+v", code, resp)
	}
	if !strings.Contains(resp["value"], "<password>") {
		t.Fatalf("expected the placeholder to remain for an interpolated value, got %q", resp["value"])
	}
}

// --- positive cases and response shape --------------------------------

// TestCopyReturnsLiteralPassword is D3b's one allowed case: a password
// written literally in the instance's source Compose file, under a known
// key, comes back for database_url, cli and env_block, and the response
// carries Cache-Control: no-store.
func TestCopyReturnsLiteralPassword(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	sourceYAML := `
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: appuser
      POSTGRES_DB: appdb
      POSTGRES_PASSWORD: literal-canary-pw
`
	copyFixture(t, newBase, "sf-copylit-1", sourceYAML, copyFixtureConfig)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55973)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55973"
	headers := map[string]string{"X-Stackfork-Token": srv.Token, "Origin": "http://" + host}

	code, resp := postCopy(t, srv, host, headers, map[string]string{"project": "sf-copylit-1", "service": "db", "kind": "database_url"})
	if code != 200 {
		t.Fatalf("database_url: status %d, body %+v", code, resp)
	}
	want := "postgres://appuser:literal-canary-pw@127.0.0.1:20200/appdb"
	if resp["value"] != want {
		t.Errorf("database_url: got %q, want %q", resp["value"], want)
	}

	code, resp = postCopy(t, srv, host, headers, map[string]string{"project": "sf-copylit-1", "service": "db", "kind": "cli"})
	if code != 200 || !strings.Contains(resp["value"], "literal-canary-pw") {
		t.Errorf("cli: status %d, body %+v", code, resp)
	}

	code, resp = postCopy(t, srv, host, headers, map[string]string{"project": "sf-copylit-1", "kind": "env_block"})
	if code != 200 || !strings.Contains(resp["value"], "DATABASE_URL=postgres://appuser:literal-canary-pw@") {
		t.Errorf("env_block: status %d, body %+v", code, resp)
	}

	req := httptest.NewRequest("POST", "/api/copy", bytes.NewReader(mustJSON(t, map[string]string{"project": "sf-copylit-1", "service": "db", "kind": "database_url"})))
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// TestCopyEnvBlockKeepsEachServicesOwnPassword covers a stack with two
// different kinds (postgres + redis, distinct literal passwords): the
// env_block copy must substitute each line with ITS OWN service's
// password, never mix them up.
func TestCopyEnvBlockKeepsEachServicesOwnPassword(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	sourceYAML := `
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: appuser
      POSTGRES_PASSWORD: pg-secret-1
  cache:
    image: redis:7
    command: ["redis-server", "--requirepass", "redis-secret-2"]
`
	config := `{
  "services": {
    "db": {
      "image": "postgres:17",
      "ports": [{"target": 5432, "published": "20210", "protocol": "tcp"}],
      "environment": {"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "pg-secret-1"}
    },
    "cache": {
      "image": "redis:7",
      "ports": [{"target": 6379, "published": "20211", "protocol": "tcp"}],
      "command": ["redis-server", "--requirepass", "redis-secret-2"]
    }
  }
}`
	copyFixture(t, newBase, "sf-copymix-1", sourceYAML, config)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55974)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55974"
	code, resp := postCopy(t, srv, host, map[string]string{
		"X-Stackfork-Token": srv.Token, "Origin": "http://" + host,
	}, map[string]string{"project": "sf-copymix-1", "kind": "env_block"})
	if code != 200 {
		t.Fatalf("status %d, body %+v", code, resp)
	}
	if !strings.Contains(resp["value"], "DATABASE_URL=postgres://appuser:pg-secret-1@127.0.0.1:20210/appuser") {
		t.Errorf("DATABASE_URL line wrong: %q", resp["value"])
	}
	if !strings.Contains(resp["value"], "REDIS_URL=redis://:redis-secret-2@127.0.0.1:20211") {
		t.Errorf("REDIS_URL line wrong: %q", resp["value"])
	}
}

// TestCopyPlaceholderStillInInstancesAndListJSON proves the copy endpoint's
// existence doesn't change any other surface: GET /api/instances and
// `list --json` keep the placeholder, even for the exact service the copy
// endpoint can return a real password for.
func TestCopyPlaceholderStillInInstancesAndListJSON(t *testing.T) {
	installFakeDocker(t)
	newBase, legacyBase := t.TempDir(), t.TempDir()
	sourceYAML := `
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: appuser
      POSTGRES_PASSWORD: literal-canary-pw
`
	copyFixture(t, newBase, "sf-copyplaceholder-1", sourceYAML, copyFixtureConfig)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55975)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55975"

	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /api/instances: status %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "literal-canary-pw") {
		t.Fatalf("GET /api/instances leaked the real password")
	}
	// json.Encoder HTML-escapes "<"/">" by default, so the placeholder
	// shows up as <password> in the raw JSON - still definitely
	// not the real password.
	if !strings.Contains(rec.Body.String(), "\\u003cpassword\\u003e") {
		t.Fatalf("GET /api/instances missing the placeholder:\n%s", rec.Body.String())
	}

	var listOut, listErr bytes.Buffer
	code := cli.Run([]string{"--state-dir", newBase, "--legacy-state-dir", legacyBase, "list", "--json"}, &listOut, &listErr)
	if code != 0 {
		t.Fatalf("list --json failed: %s", listErr.String())
	}
	if strings.Contains(listOut.String(), "literal-canary-pw") {
		t.Fatalf("list --json leaked the real password")
	}
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
