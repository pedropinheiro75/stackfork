package dashboard_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
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
	if code := cli.Run([]string{"--root", root, "--state-dir", newBase, "prepare", "--force", "-f", composePath}, &out, &errBuf); code != 0 {
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

func TestSeedCalloutOffersRetryMarkDoneAndForce(t *testing.T) {
	srv, newBase, project, _ := seedStack(t)
	m := manifestOf(t, newBase, project)
	m.Seeded = boolp(false)
	state.WritePrivate(filepath.Join(newBase, project, "manifest.json"), m)
	os.WriteFile(filepath.Join(newBase, project, state.SeedLog), []byte("npm: not found\n"), 0o600)
	body := indexBody(srv)
	run := "stackfork --instance " + project + " seed"
	for _, want := range []string{
		`data-seed="failed"`, "Database seed failed", `<pre class="mono-log"`, "npm: not found",
		`data-run="` + run + `"`, `>Retry seed</button>`,
		`data-run="` + run + ` --mark-done"`, "Mark as seeded",
		`data-run="` + run + ` --force"`, "Re-seed, overwriting data",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// the overwriting item is the danger one and comes last; Retry is not in the menu
	force := strings.Index(body, `data-run="`+run+` --force"`)
	if tag := body[strings.LastIndex(body[:force], "<button"):force]; !strings.Contains(tag, "menu-item-danger") {
		t.Errorf("the force item must be danger: %s", tag)
	}
	if done := strings.Index(body, `--mark-done"`); done > force {
		t.Error("Mark as seeded must come before the danger item")
	}
	if strings.Contains(body, `data-action="seed`) {
		t.Error("seed goes through /api/run, not an action")
	}
	if js, _ := os.ReadFile("static/app.js"); !strings.Contains(string(js), "inst.seed_done") || !strings.Contains(string(js), "inst.seed_error") {
		t.Error("app.js must render seed_done and seed_error like the template")
	}
}

func TestStoppedStateIsOffNotFailed(t *testing.T) {
	srv, _, _, _ := seedStack(t)
	body := indexBody(srv)
	chip := regexp.MustCompile(`(?s)<header class="stack-header">.*?<span class="chip chip-(\w+)"[^>]*><span class="chip-glyph"[^>]*>[^<]*</span>Stopped</span>`).FindStringSubmatch(body)
	if chip == nil || chip[1] != "off" {
		t.Fatalf("a stopped instance reads Stopped in the off tone, got %v", chip)
	}
	card := body[strings.Index(body, `<article class="stack`):]
	card = card[:strings.Index(card, `<div class="stack-body"`)]
	if strings.Contains(card, "chip-failed") {
		t.Errorf("a stopped card shows no failed chip:\n%s", card)
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

func TestStatusDotsFollowHealthAndOneOffsShowApart(t *testing.T) {
	srv, _, project, _ := seedStack(t)
	ps := func(service, name, state, status string, oneoff bool) string {
		l := "com.docker.compose.project=" + project + ",com.docker.compose.service=" + service + ",io.stackfork.root=/x,com.docker.compose.oneoff=" + map[bool]string{true: "True", false: "False"}[oneoff]
		b, _ := json.Marshal(map[string]string{"ID": name, "Names": name, "Labels": l, "State": state, "Status": status})
		return string(b)
	}
	t.Setenv("DOCKER_FAKE_PS", strings.Join([]string{
		ps("db", project+"-db-1", "running", "Up 1 hour (unhealthy)", false),
		ps("db", project+"-db-run-1", "created", "Created", true),
		ps("db", project+"-db-run-2", "running", "Up 2 minutes", true),
	}, "\n")+"\n")
	body := indexBody(srv)
	for _, want := range []string{`chip chip-attention" title="running, unhealthy"`, "Unhealthy", "1 one-off task running", "1 leftover one-off container", `data-run="docker rm ` + project + `-db-run-1"`, "db unhealthy"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `chip-ok" title="running`) {
		t.Error("an unhealthy service must not be green")
	}
	js, _ := os.ReadFile("static/app.js")
	css, _ := os.ReadFile("static/app.css")
	for _, want := range []string{"function serviceChip", "oneOffHtml(inst)"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	for _, want := range []string{".chip-attention", ".chip-busy", ".chip-off", ".callout-attention"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("css lacks %s", want)
		}
	}
}

// A cold, slow Docker never delays the page: it renders at once in its
// loading state, and /api/instances (what app.js polls) fills it in.
func TestIndexRendersLoadingStateWhenDockerIsSlow(t *testing.T) {
	srv, _, project, _ := seedStack(t)
	old := dashboard.IndexWait
	dashboard.IndexWait = 50 * time.Millisecond
	defer func() { dashboard.IndexWait = old }()
	t.Setenv("DOCKER_FAKE_PS_SLEEP", "1")
	start := time.Now()
	body := indexBody(srv)
	if d := time.Since(start); d > 700*time.Millisecond {
		t.Fatalf("page took %v", d)
	}
	for _, want := range []string{`id="loading-note"`, "loading...", "checking&hellip;"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `<article class="stack"`) || strings.Contains(body, "Docker &middot; unreachable") {
		t.Errorf("loading page must not show cards or a docker error:\n%s", body)
	}
	// the data arrives through the API the script polls
	req := httptest.NewRequest("GET", "/api/instances", nil)
	req.Host = seedHost
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), project) {
		t.Fatalf("api: %s", rec.Body.String())
	}
	if js, _ := os.ReadFile("static/app.js"); !strings.Contains(string(js), "    poll();\n    setInterval") {
		t.Error("app.js must poll at once on start")
	}
}
