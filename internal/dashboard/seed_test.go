package dashboard_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const seedPort = 55331

func seedPost(srv *dashboard.Server, body map[string]string, noToken bool, origin string) *httptest.ResponseRecorder {
	host := "127.0.0.1:55331"
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/actions", bytes.NewReader(b))
	req.Host = host
	if !noToken {
		req.Header.Set("X-Stackfork-Token", srv.Token)
	}
	req.Header.Set("Origin", "http://"+host)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func seedInstance(t *testing.T, script string) (srv *dashboard.Server, newBase, project, root string) {
	t.Helper()
	newBase, legacy, project := preparedFixture(t)
	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil || m == nil {
		t.Fatal(err)
	}
	root = m.Root
	pkg := `{"scripts":{` + script + `}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err = dashboard.New(newBase, legacy, "test", seedPort)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func manifestOf(t *testing.T, newBase, project string) *state.Manifest {
	m, err := state.ReadManifest(filepath.Join(newBase, project, "manifest.json"))
	if err != nil || m == nil {
		t.Fatal(err)
	}
	return m
}

func indexBody(srv *dashboard.Server) string {
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "127.0.0.1:55331"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}

func TestSeedActionsNeedTokenAndOrigin(t *testing.T) {
	srv, newBase, project, _ := seedInstance(t, `"db:seed":"x"`)
	for _, act := range []string{"seed-now", "seed-never", "recipe-seed"} {
		body := map[string]string{"action": act, "project": project, "choice": "never"}
		if rec := seedPost(srv, body, true, ""); rec.Code != 403 {
			t.Errorf("%s without token: %d", act, rec.Code)
		}
		if rec := seedPost(srv, body, false, "http://evil.example"); rec.Code != 403 {
			t.Errorf("%s foreign origin: %d", act, rec.Code)
		}
	}
	if m := manifestOf(t, newBase, project); m.SeedOnce != "" || m.Seeded != nil {
		t.Fatalf("a refused request changed the manifest: %+v", m)
	}
}

func TestSeedNowRunsDetectedCommandOnceAndForceStaysCopyOnly(t *testing.T) {
	srv, newBase, project, _ := seedInstance(t, `"db:seed":"x"`)
	if body := indexBody(srv); !strings.Contains(body, `data-seed="proposal"`) || !strings.Contains(body, `data-action="seed-now"`) || !strings.Contains(body, `data-action="seed-never"`) {
		t.Fatalf("no decision prompt:\n%s", body)
	}
	// the client cannot choose the command
	rec := seedPost(srv, map[string]string{"action": "seed-now", "project": project, "service": "db", "choice": "rm -rf /"}, false, "")
	var resp actionResponseFixture
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != 200 || !resp.OK {
		t.Fatalf("seed-now: %d %s", rec.Code, rec.Body.String())
	}
	m := manifestOf(t, newBase, project)
	if m.SeedOnce != "npm run db:seed" || m.Seeded == nil || !*m.Seeded {
		t.Fatalf("manifest %+v", m)
	}
	body := indexBody(srv)
	if !strings.Contains(body, `class="seed-badge"`) || !strings.Contains(body, "seed --force") || strings.Contains(body, `data-action="seed-now"`) {
		t.Fatalf("seeded card:\n%s", body)
	}
	rec = seedPost(srv, map[string]string{"action": "seed-now", "project": project}, false, "")
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.OK || !strings.Contains(resp.Message, "seed --force") {
		t.Fatalf("a second seed-now must be refused with the copy command: %+v", resp)
	}
}

func TestSeedNeverAndPendingRender(t *testing.T) {
	srv, newBase, project, _ := seedInstance(t, `"db:seed":"x"`)
	if rec := seedPost(srv, map[string]string{"action": "seed-never", "project": project}, false, ""); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if m := manifestOf(t, newBase, project); m.SeedOnce != "none" {
		t.Fatalf("manifest %+v", m)
	}
	if body := indexBody(srv); strings.Contains(body, `data-seed=`) {
		t.Fatalf("a declined project still prompts:\n%s", body)
	}
	// decided but not seeded yet: "database not seeded" with a button
	p := filepath.Join(newBase, project, "manifest.json")
	m := manifestOf(t, newBase, project)
	f := false
	m.SeedOnce, m.Seeded = "npm run db:seed", &f
	state.WritePrivate(p, m)
	time.Sleep(2100 * time.Millisecond) // the inventory cache TTL
	if body := indexBody(srv); !strings.Contains(body, `data-seed="pending"`) || !strings.Contains(body, "database not seeded") {
		t.Fatalf("no pending bar:\n%s", body)
	}
}

func TestSeedControlsEscapeHostileCommands(t *testing.T) {
	srv, _, _, _ := seedInstance(t, `"db:seed\"><img src=x onerror=alert(1)>":"x"`)
	body := indexBody(srv)
	if strings.Contains(body, "<img src=x") || strings.Contains(body, `onerror=alert(1)>`) {
		t.Fatalf("unescaped command in the page:\n%s", body)
	}
	if !strings.Contains(body, "&lt;img src=x") {
		t.Fatalf("hostile command not rendered escaped:\n%s", body)
	}
	// the page script builds the same markup client-side from JSON: it must escape too
	js, _ := os.ReadFile("static/app.js")
	for _, want := range []string{"escapeHtml(inst.seed_proposal)", "escapeHtml(inst.seed_command)"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js does not escape %s", want)
		}
	}
}

func TestRecipeSeedFromReposTab(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("svc")
	os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"db:migrate":"m","db:seed":"s"}}`), 0o644)
	other := scratchRepoAt(t, filepath.Join(e.root, "other"))
	e.get("/api/repos")

	frag := e.get("/fragments/repos").Body.String()
	for _, want := range []string{"Startup recipe", "npm run db:migrate", "not decided yet", `data-recipe-set`, "Seed with npm run db:seed"} {
		if !strings.Contains(frag, want) {
			t.Errorf("fragment missing %q", want)
		}
	}
	post := func(r, choice string, o reqOpts) *httptest.ResponseRecorder {
		return e.post("/api/actions", map[string]string{"action": "recipe-seed", "repo": r, "choice": choice}, o)
	}
	if post(repo, "detected", reqOpts{noToken: true}).Code != 403 || post(repo, "detected", reqOpts{origin: "http://evil.example"}).Code != 403 {
		t.Error("token/origin not enforced")
	}
	if post(t.TempDir(), "detected", reqOpts{}).Code != 403 {
		t.Error("an unscanned repo was accepted")
	}
	if post(repo, "rm -rf /", reqOpts{}).Code != 400 {
		t.Error("unknown choice accepted")
	}
	// no knowledge file and no instance: nothing to record into
	var resp actionResponseFixture
	json.Unmarshal(post(repo, "detected", reqOpts{}).Body.Bytes(), &resp)
	if resp.OK || !strings.Contains(resp.Message, "no knowledge file") {
		t.Fatalf("%+v", resp)
	}
	// with a knowledge file it lands in the recipe, and nothing runs
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil && os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755) == nil {
		k.Save(filepath.Join(repo, ".stackfork", "knowledge.json"))
	}
	json.Unmarshal(post(repo, "detected", reqOpts{}).Body.Bytes(), &resp)
	got, _ := knowledge.Load(filepath.Join(repo, ".stackfork", "knowledge.json"))
	if !resp.OK || got == nil || got.Recipe.SeedOnce != "npm run db:seed" || got.Recipe.SeedPending {
		t.Fatalf("%+v %+v", resp, got)
	}
	e.srv.Wait()
	if !strings.Contains(e.get("/fragments/repos").Body.String(), "<code>npm run db:seed</code>") {
		t.Error("decision not shown")
	}
	post(repo, "never", reqOpts{})
	if got, _ := knowledge.Load(filepath.Join(repo, ".stackfork", "knowledge.json")); got.Recipe.SeedOnce != "none" {
		t.Errorf("never: %+v", got.Recipe)
	}
	if len(e.started()) != 0 {
		t.Error("recording a decision started something")
	}
	_ = other
}
