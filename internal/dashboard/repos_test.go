package dashboard_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const reposPort = 55301
const reposHost = "127.0.0.1:55301"

type browserEnv struct {
	t      *testing.T
	srv    *dashboard.Server
	root   string // scanned root (resolved)
	newDir string
	home   string
	mu     sync.Mutex
	starts []string
}

func (e *browserEnv) started() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.starts...)
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.email=t@e.x", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// scratchRepoAt makes a one-commit repo (main) at dir and returns its
// resolved path.
func scratchRepoAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	gitIn(t, dir, "add", "f")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	r, _ := filepath.EvalSymlinks(dir)
	return r
}

// newBrowserEnv builds a dashboard over temp config/state dirs and a temp
// root that is already configured as a scan root.
func newBrowserEnv(t *testing.T) *browserEnv {
	t.Helper()
	installFakeDocker(t)
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	e := &browserEnv{t: t, newDir: t.TempDir(), home: home}
	e.root, _ = filepath.EvalSymlinks(t.TempDir())
	var out, errBuf bytes.Buffer
	if code := cli.Run([]string{"config", "add-root", e.root}, &out, &errBuf); code != 0 {
		t.Fatalf("add-root: %s", errBuf.String())
	}
	srv, err := dashboard.New(e.newDir, t.TempDir(), "test", reposPort)
	if err != nil {
		t.Fatal(err)
	}
	srv.StartEnv = func(out, errOut io.Writer, checkout string) error {
		e.mu.Lock()
		e.starts = append(e.starts, checkout)
		e.mu.Unlock()
		io.WriteString(out, "preparing it\nstarting it\n")
		return nil
	}
	e.srv = srv
	t.Cleanup(srv.Wait)
	return e
}

func (e *browserEnv) readyRepo(name string) string {
	repo := scratchRepoAt(e.t, filepath.Join(e.root, name))
	os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(fixtureCompose), 0o644)
	if err := (&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		e.t.Fatal(err)
	}
	return repo
}

// needsLearnRepo could run (it has an adapter) but nothing verified how.
func (e *browserEnv) needsLearnRepo(name string) string {
	repo := scratchRepoAt(e.t, filepath.Join(e.root, name))
	os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(fixtureCompose), 0o644)
	return repo
}

type reqOpts struct {
	noToken bool
	origin  string
}

func (e *browserEnv) post(path string, body interface{}, o reqOpts) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Host = reposHost
	if !o.noToken {
		req.Header.Set("X-Stackfork-Token", e.srv.Token)
	}
	req.Header.Set("Origin", "http://"+reposHost)
	if o.origin != "" {
		req.Header.Set("Origin", o.origin)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *browserEnv) get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.Host = reposHost
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestReposAPIIsByteIdenticalToCLI(t *testing.T) {
	e := newBrowserEnv(t)
	e.readyRepo("alpha")
	scratchRepoAt(t, filepath.Join(e.root, "Group", "beta"))

	var cliOut, cliErr bytes.Buffer
	if code := cli.Run([]string{"--state-dir", e.newDir, "--legacy-state-dir", t.TempDir(), "repos", "--json"}, &cliOut, &cliErr); code != 0 {
		t.Fatal(cliErr.String())
	}
	rec := e.get("/api/repos")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != cliOut.String() {
		t.Fatalf("repos --json and /api/repos differ:\ncli: %s\napi: %s", cliOut.String(), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"readiness": "ready"`) || !strings.Contains(rec.Body.String(), `"group": "Group"`) {
		t.Fatalf("unexpected content: %s", rec.Body.String())
	}
}

func TestRepoActionsRefuseAnythingNotScanned(t *testing.T) {
	e := newBrowserEnv(t)
	ready := e.readyRepo("ready")
	unknown := scratchRepoAt(t, filepath.Join(e.root, "mystery"))
	gitIn(t, ready, "branch", "idle")
	outside := scratchRepoAt(t, filepath.Join(t.TempDir(), "outside"))
	plainDir := filepath.Join(e.root, "not-a-repo")
	os.MkdirAll(plainDir, 0o755)
	e.get("/api/repos") // load the scan

	type req = map[string]string
	cases := []struct {
		name string
		body req
		opts reqOpts
		want int
	}{
		{"no token", req{"action": "start", "repo": ready, "path": ready}, reqOpts{noToken: true}, 403},
		{"foreign origin", req{"action": "start", "repo": ready, "path": ready}, reqOpts{origin: "http://evil.example"}, 403},
		{"repo outside the roots", req{"action": "start", "repo": outside, "path": outside}, reqOpts{}, 403},
		{"folder under a root that is not a repo", req{"action": "fetch", "repo": plainDir}, reqOpts{}, 403},
		{"root itself", req{"action": "fetch", "repo": e.root}, reqOpts{}, 403},
		{"path of another repo", req{"action": "start", "repo": ready, "path": unknown}, reqOpts{}, 403},
		{"path that is not a worktree", req{"action": "start", "repo": ready, "path": filepath.Join(ready, "sub")}, reqOpts{}, 403},
		{"worktree-add by path", req{"action": "worktree-add", "repo": ready, "path": ready}, reqOpts{}, 403},
		{"option-like branch", req{"action": "worktree-add", "repo": ready, "branch": "-x"}, reqOpts{}, 400},
		{"bad ref name", req{"action": "worktree-add", "repo": ready, "branch": "a..b"}, reqOpts{}, 400},
		{"shell metacharacters", req{"action": "start", "repo": ready, "branch": "$(touch pwned)"}, reqOpts{}, 400},
		{"branch not in the scan", req{"action": "worktree-add", "repo": ready, "branch": "ghost"}, reqOpts{}, 403},
		{"start on an unknown repo", req{"action": "start", "repo": unknown, "path": unknown}, reqOpts{}, 409},
		{"empty action fields", req{"action": "start", "repo": ""}, reqOpts{}, 403},
	}
	for _, c := range cases {
		rec := e.post("/api/actions", c.body, c.opts)
		if rec.Code != c.want {
			t.Errorf("%s: got %d (%s), want %d", c.name, rec.Code, strings.TrimSpace(rec.Body.String()), c.want)
		}
	}
	if got := e.started(); len(got) != 0 {
		t.Fatalf("a refused request started something: %v", got)
	}
	if _, err := os.Stat(filepath.Join(ready, ".worktrees")); err == nil {
		t.Fatal("a refused request created a worktree")
	}
	if _, err := os.Stat("pwned"); err == nil {
		t.Fatal("shell metacharacters were interpreted")
	}
}

func ndjson(t *testing.T, body string) (lines []string, final map[string]interface{}) {
	t.Helper()
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not ndjson: %q", l)
		}
		if s, ok := m["line"].(string); ok {
			lines = append(lines, s)
		} else {
			final = m
		}
	}
	return
}

func TestStartExistingWorktreeAndBranchWithoutOne(t *testing.T) {
	e := newBrowserEnv(t)
	repo := e.readyRepo("ready")
	gitIn(t, repo, "branch", "idle")
	e.get("/api/repos")

	rec := e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "path": repo}, reqOpts{})
	lines, final := ndjson(t, rec.Body.String())
	if rec.Code != 200 || final["ok"] != true || len(lines) != 2 || lines[0] != "preparing it" {
		t.Fatalf("%d lines=%v final=%v", rec.Code, lines, final)
	}
	if got := e.started(); len(got) != 1 || got[0] != repo {
		t.Fatalf("started %v", got)
	}

	// a branch with no worktree: the worktree is created, then started
	rec = e.post("/api/actions", map[string]string{"action": "start", "repo": repo, "branch": "idle"}, reqOpts{})
	lines, final = ndjson(t, rec.Body.String())
	wt := filepath.Join(repo, ".worktrees", "idle")
	if final["ok"] != true || !strings.Contains(strings.Join(lines, "|"), "created "+wt) {
		t.Fatalf("lines=%v final=%v", lines, final)
	}
	if got := e.started(); len(got) != 2 || got[1] != wt {
		t.Fatalf("started %v", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "f")); err != nil {
		t.Fatal("worktree not created")
	}
}

func TestWorktreeAddAndFetchActions(t *testing.T) {
	e := newBrowserEnv(t)
	repo := scratchRepoAt(t, filepath.Join(e.root, "proj"))
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, repo, "remote", "add", "origin", origin)
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")
	gitIn(t, repo, "branch", "idle")
	e.get("/api/repos")

	rec := e.post("/api/actions", map[string]string{"action": "worktree-add", "repo": repo, "branch": "idle"}, reqOpts{})
	var resp struct {
		OK      bool
		Message string
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != 200 || !resp.OK || !strings.Contains(resp.Message, filepath.Join(repo, ".worktrees", "idle")) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if got := e.started(); len(got) != 0 {
		t.Fatalf("worktree-add must not start anything: %v", got)
	}
	rec = e.post("/api/actions", map[string]string{"action": "fetch", "repo": repo}, reqOpts{})
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != 200 || !resp.OK {
		t.Fatalf("fetch: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRepoFragmentEscapesHostileNamesAndOffersNoDestructiveButton(t *testing.T) {
	e := newBrowserEnv(t)
	hostile := scratchRepoAt(t, filepath.Join(e.root, `<img src=x onerror=alert(1)>`))
	gitIn(t, hostile, "branch", `x"onmouseover="alert(2)`)
	gitIn(t, hostile, "branch", `<b>bold</b>`)
	ready := e.readyRepo("ready")
	gitIn(t, ready, "worktree", "add", "-q", "-b", "wt", filepath.Join(ready, ".worktrees", "wt"))
	e.needsLearnRepo("adapted")

	body := e.get("/fragments/repos").Body.String()
	for _, raw := range []string{"<img src=x", `x"onmouseover=`, "<b>bold</b>", "onerror=alert(1)>"} {
		if strings.Contains(body, raw) {
			t.Errorf("unescaped hostile text %q in:\n%s", raw, body)
		}
	}
	for _, want := range []string{"&lt;img src=x", "&lt;b&gt;bold&lt;/b&gt;", "Learn with your agent", `data-repo-action="start"`, "readiness-unknown", "readiness-ready", "readiness-needs-learn", ">needs learn<"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// one learn button per repo that is not ready (hostile + adapted), not per row
	if n := strings.Count(body, ">Learn with your agent<"); n != 2 {
		t.Errorf("want one learn button per non-ready repo, got %d", n)
	}
	// unknown and needs-learn repos get no Start button; ready ones do
	if strings.Count(body, `data-repo-action="start"`) != 2 { // ready: main checkout + wt
		t.Errorf("expected Start only for the ready repo's two worktrees:\n%s", body)
	}
	// removing a worktree is a copyable command, never a button/action
	if !strings.Contains(body, "worktree remove") || strings.Contains(body, `data-repo-action="remove`) || strings.Contains(body, "worktree-remove") {
		t.Errorf("remove must be copy-only")
	}
}

func TestFirstRunShowsSettingsHintAndAddFolderField(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	srv, _ := dashboard.New(t.TempDir(), t.TempDir(), "test", reposPort)
	t.Cleanup(srv.Wait)
	e := &browserEnv{srv: srv, t: t}
	body := e.get("/fragments/repos").Body.String()
	if !strings.Contains(body, `data-configured="false"`) || !strings.Contains(body, "stackfork config add-root PATH") {
		t.Fatalf("empty state:\n%s", body)
	}
	settings := e.get("/fragments/settings").Body.String()
	if !strings.Contains(settings, "Add folder") || !strings.Contains(settings, "stackfork config add-root PATH") {
		t.Fatalf("settings:\n%s", settings)
	}
}

func TestConfigAPIValidationAndSecurity(t *testing.T) {
	e := newBrowserEnv(t)
	file := filepath.Join(e.root, "afile")
	os.WriteFile(file, nil, 0o644)
	nested := filepath.Join(e.root, "inner")
	os.MkdirAll(nested, 0o755)
	other, _ := filepath.EvalSymlinks(t.TempDir())

	cfgBytes := func() string {
		out, _, _ := runCLIJSON(t, "config", "get")
		return out
	}
	before := cfgBytes()
	body := func(op, v string) map[string]string { return map[string]string{"op": op, "value": v} }

	if rec := e.post("/api/config", body("add-root", other), reqOpts{noToken: true}); rec.Code != 403 {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := e.post("/api/config", body("add-root", other), reqOpts{origin: "http://evil.example"}); rec.Code != 403 {
		t.Errorf("foreign origin: %d", rec.Code)
	}
	for name, v := range map[string]string{
		"filesystem root": "/", "bare home": e.home, "tilde home": "~", "missing": filepath.Join(other, "nope"),
		"a file": file, "inside an existing root": nested, "relative": "some/dir", "empty": "",
	} {
		rec := e.post("/api/config", body("add-root", v), reqOpts{})
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"ok":false`) {
			t.Errorf("%s: got %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := e.post("/api/config", body("nonsense", ""), reqOpts{}); rec.Code != 400 {
		t.Errorf("unknown op: %d", rec.Code)
	}
	if cfgBytes() != before {
		t.Fatal("a refused request changed the config")
	}

	// a valid root, an exclude and a depth are saved, and the CLI sees them
	for _, c := range [][2]string{{"add-root", other}, {"add-exclude", "Backup"}, {"set-max-depth", "4"}} {
		if rec := e.post("/api/config", body(c[0], c[1]), reqOpts{}); rec.Code != 200 {
			t.Fatalf("%v: %d %s", c, rec.Code, rec.Body.String())
		}
	}
	got := cfgBytes()
	for _, want := range []string{other, "Backup", `"maxDepth": 4`} {
		if !strings.Contains(got, want) {
			t.Errorf("config get misses %q:\n%s", want, got)
		}
	}
	// removing a root deletes nothing on disk
	if rec := e.post("/api/config", body("remove-root", other), reqOpts{}); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("remove-root touched the disk")
	}
	if strings.Contains(cfgBytes(), other) {
		t.Fatal("root still configured")
	}
}

func runCLIJSON(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := cli.Run(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}
