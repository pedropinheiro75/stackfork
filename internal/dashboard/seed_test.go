package dashboard_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const seedHost = "127.0.0.1:55331"

func seedPost(srv *dashboard.Server, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/actions", bytes.NewReader(b))
	req.Host = seedHost
	req.Header.Set("X-Stackfork-Token", srv.Token)
	req.Header.Set("Origin", "http://"+seedHost)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func indexBody(srv *dashboard.Server) string {
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = seedHost
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}

// seedStack prepares an instance with a named volume in a checkout that has
// a seed script.
func seedStack(t *testing.T) (srv *dashboard.Server, newBase, project, root string) {
	t.Helper()
	installFakeDocker(t)
	root = t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte("services:\n  db:\n    image: postgres:17\n    volumes:\n      - pg:/data\nvolumes:\n  pg: {}\n"), 0o600)
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"db:migrate":"m","db:seed":"s"}}`), 0o644)
	newBase = t.TempDir()
	var out, errBuf bytes.Buffer
	if code := cli.Run([]string{"--root", root, "--state-dir", newBase, "prepare", "-f", composePath}, &out, &errBuf); code != 0 {
		t.Fatalf("prepare: %s", errBuf.String())
	}
	var p struct{ Project string }
	json.Unmarshal(out.Bytes(), &p)
	project = p.Project
	srv, err := dashboard.New(newBase, t.TempDir(), "test", 55331)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func manifestOf(t *testing.T, newBase, project string) *state.Manifest {
	t.Helper()
	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil || m == nil {
		t.Fatal(err)
	}
	return m
}

func TestInstancesViewHasNoSeedUI(t *testing.T) {
	srv, newBase, project, _ := seedStack(t)
	for _, seeded := range []*bool{nil, boolp(true)} {
		m := manifestOf(t, newBase, project)
		m.Seeded = seeded
		state.WritePrivate(filepath.Join(newBase, project, "manifest.json"), m)
		time.Sleep(2100 * time.Millisecond) // the inventory cache TTL
		body := indexBody(srv)
		for _, bad := range []string{"seed-bar", "Seed now", "Never for this project", "seed-badge", "Seed the new database", "data-seed"} {
			if strings.Contains(body, bad) {
				t.Errorf("seeded=%v: the card shows %q", seeded, bad)
			}
		}
	}
	// the old actions are gone
	for _, act := range []string{"seed-now", "seed-never", "recipe-seed"} {
		if rec := seedPost(srv, map[string]string{"action": act, "project": project}); rec.Code != 400 {
			t.Errorf("%s still exists: %d", act, rec.Code)
		}
	}
	if js, _ := os.ReadFile("static/app.js"); strings.Contains(string(js), "seed-now") || strings.Contains(string(js), "seed_proposal") {
		t.Error("app.js still has the removed seed controls")
	}
}

func boolp(b bool) *bool { return &b }

func TestFailedSeedShowsRetryAndCopyOnlyForce(t *testing.T) {
	srv, newBase, project, _ := seedStack(t)
	m := manifestOf(t, newBase, project)
	m.Seeded = boolp(false)
	state.WritePrivate(filepath.Join(newBase, project, "manifest.json"), m)
	body := indexBody(srv)
	run := "stackfork --instance " + project + " seed"
	for _, want := range []string{`data-seed="failed"`, `data-copy="` + run + `"`, `data-copy="` + run + ` --force"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `data-action="seed`) {
		t.Error("the failure warning must not offer a button")
	}
}

func TestDashboardUpSeedsANewVolumeOnce(t *testing.T) {
	srv, newBase, project, _ := seedStack(t)
	log := filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("DOCKER_FAKE_LOG", log)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	for i := 0; i < 2; i++ {
		if rec := seedPost(srv, map[string]string{"action": "up", "project": project}); rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
	}
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "npm run db:seed"); n != 1 {
		t.Fatalf("seeded %d times:\n%s", n, b)
	}
	if m := manifestOf(t, newBase, project); m.Seeded == nil || !*m.Seeded {
		t.Fatalf("manifest %+v", m)
	}
}

func TestReposTabShowsReadOnlyRecipe(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"db:migrate":"m","db:seed\"><img src=x onerror=alert(1)>":"s"}}`), 0o644)
	e.get("/api/repos")
	frag := e.get("/fragments/repos").Body.String()
	if strings.Contains(frag, "<img src=x") || !strings.Contains(frag, "&lt;img src=x") {
		t.Fatalf("hostile script name not escaped:\n%s", frag)
	}
	for _, want := range []string{"Startup recipe", "npm run db:migrate", "auto (detected", "stackfork learn record recipe --seed-once"} {
		if !strings.Contains(frag, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"recipe-choice", "data-recipe-set", "<select"} {
		if strings.Contains(frag, bad) {
			t.Errorf("recipe is read-only, found %q", bad)
		}
	}
	// a recorded decision is shown, never changed from the page
	os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Recipe: knowledge.Recipe{SeedOnce: "none"}}
	k.Save(filepath.Join(repo, ".stackfork", "knowledge.json"))
	if frag := e.get("/fragments/repos").Body.String(); !strings.Contains(frag, "seed: never") {
		t.Errorf("never not shown:\n%s", frag)
	}
	if rec := e.post("/api/actions", map[string]string{"action": "recipe-seed", "repo": repo}, reqOpts{}); rec.Code != 400 {
		t.Errorf("recipe-seed action still exists: %d", rec.Code)
	}
}
