package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

const canaryFixtureCompose = `services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: appuser
      POSTGRES_DB: appdb
      POSTGRES_PASSWORD: ${CANARY_T:-devpassword-not-canary}
    ports:
      - target: 5432
        published: "5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U appuser"]
      interval: 2s
      timeout: 3s
      retries: 30
`

// TestLiveInterpolatedSecretNeverWrittenToSnapshot is design.md D6's
// security fix, task from the coordinator's security batch: a real
// ${VAR}-interpolated secret, set only in this test process's own
// environment (the exact scenario `docker compose config` would otherwise
// resolve and bake into the snapshot at prepare time), must never appear
// anywhere - not compose.json, not manifest.json, not `list --json`, not
// the dashboard's /api/instances, not any command's own stdout/stderr, and
// not /api/copy. The fixture's own `${CANARY_T:-devpassword-not-canary}`
// default is a legitimate literal value once resolved (render's subprocess
// never sees CANARY_T at all, so it falls back the same as it would for
// any genuinely-unset variable) and /api/copy correctly returns *that* -
// the one thing under test is that the ambient canary itself never appears.
func TestLiveInterpolatedSecretNeverWrittenToSnapshot(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)

	const canary = "leaked-canary-9f8e7d3c1a"
	t.Setenv("CANARY_T", canary)

	before := snapshotDocker(t)
	scratch := t.TempDir()
	root := filepath.Join(scratch, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "-q", root)
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(canaryFixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}

	state := filepath.Join(scratch, "state")
	legacyState := filepath.Join(scratch, "legacy-state")
	li := liveInstance{binary: bin, root: root, state: state, legacyState: legacyState}
	defer exec.Command(li.binary, li.cli("down", "--volumes")...).Run() //nolint:errcheck // best-effort cleanup on any exit path, including a failed assertion

	// prepare/up inherit this process's real environment (CANARY_T
	// included) via mustRun -> exec.Command's default behaviour, exactly
	// like a real shell invocation would - this is the scenario being
	// fixed: CANARY_T is genuinely set wherever `prepare` runs.
	prepareOut := li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"))
	if strings.Contains(prepareOut, canary) {
		t.Fatalf("prepare's own stdout leaked the canary:\n%s", prepareOut)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(prepareOut), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, prepareOut)
	}
	project, _ := prepared["project"].(string)
	if project == "" {
		t.Fatal("expected a project name from prepare")
	}
	instanceDir := filepath.Join(state, project)

	// The snapshot itself: the canary must never appear, and the
	// interpolation must survive literally.
	composeJSON, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(composeJSON, []byte(canary)) {
		t.Fatalf("compose.json contains the canary value - the security fix did not work:\n%s", composeJSON)
	}
	// render's subprocess environment is a minimal allowlist (design.md D6),
	// not this test process's ambient environment - CANARY_T is invisible to
	// it, so Compose falls back to the fixture's own default instead of the
	// ambient value, same as it would for any genuinely-unset variable.
	if !bytes.Contains(composeJSON, []byte(`"POSTGRES_PASSWORD": "devpassword-not-canary"`)) {
		t.Fatalf("expected compose.json to fall back to the fixture default (never the ambient CANARY_T), got:\n%s", composeJSON)
	}
	manifestJSON, err := os.ReadFile(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifestJSON, []byte(canary)) {
		t.Fatalf("manifest.json contains the canary value:\n%s", manifestJSON)
	}

	upOut := li.run(t, "up", "--wait-timeout", "90")
	if strings.Contains(upOut, canary) {
		t.Fatalf("up's own stdout leaked the canary:\n%s", upOut)
	}

	// `list --json` and `where`/`status`/`urls` - every plain CLI read.
	var listOut, listErr bytes.Buffer
	if code := Run([]string{"--state-dir", state, "--legacy-state-dir", legacyState, "list", "--json"}, &listOut, &listErr); code != 0 {
		t.Fatalf("list --json failed: %s", listErr.String())
	}
	if strings.Contains(listOut.String(), canary) {
		t.Fatalf("list --json leaked the canary:\n%s", listOut.String())
	}
	for _, action := range []string{"where", "status", "urls", "logs"} {
		args := []string{"--root", root, "--state-dir", state, "--legacy-state-dir", legacyState, action}
		if action == "logs" {
			args = []string{"--root", root, "--state-dir", state, "--legacy-state-dir", legacyState, "logs", "db", "--tail", "200"}
		}
		out := mustRun(t, bin, args...)
		if strings.Contains(out, canary) {
			t.Fatalf("%s leaked the canary:\n%s", action, out)
		}
	}
	t.Log("PASS: compose.json/manifest.json/prepare/up/list/where/status/urls/logs all clean")

	// The dashboard's /api/instances - same inventory, must never disagree.
	const dashHost = "127.0.0.1:55997"
	srv, err := dashboard.New(state, legacyState, "test", 55997)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Host = dashHost
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET /api/instances: status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), project) {
		t.Fatalf("expected /api/instances to actually list the instance:\n%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("GET /api/instances leaked the canary:\n%s", rec.Body.String())
	}

	// /api/copy: the rendered snapshot's POSTGRES_PASSWORD is the fixture's
	// own fallback default ("devpassword-not-canary", design.md D6 - render
	// never sees CANARY_T at all, so ${CANARY_T:-devpassword-not-canary}
	// resolves the same way it would for any genuinely-unset variable) -
	// that default is itself a literal Compose value, so /api/copy correctly
	// returns it. The one thing that must never happen, ever, is the ambient
	// canary value itself coming back.
	copyReq := map[string]string{"project": project, "service": "db", "kind": "database_url"}
	body, _ := json.Marshal(copyReq)
	req = httptest.NewRequest("POST", "/api/copy", bytes.NewReader(body))
	req.Host = dashHost
	req.Header.Set("X-Stackfork-Token", srv.Token)
	req.Header.Set("Origin", "http://"+dashHost)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST /api/copy: status %d, body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("POST /api/copy returned the ambient canary - it must only ever return a literal Compose value:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "devpassword-not-canary") {
		t.Fatalf("expected /api/copy to return the fixture's literal fallback password:\n%s", rec.Body.String())
	}
	t.Log("PASS: /api/instances and /api/copy both clean; /api/copy returned the literal fallback, never the ambient canary")

	li.run(t, "down", "--volumes")
	after := snapshotDocker(t)
	assertSameInventory(t, before, after)
}
