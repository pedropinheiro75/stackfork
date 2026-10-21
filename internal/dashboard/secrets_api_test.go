package dashboard_test

import (
	"encoding/json"
	"errors"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

const (
	canaryOp       = "op-canary-api-7-1"
	canaryKeychain = "kc-canary-api-7-1"
)

type apiSecret struct {
	Name, Reference, Source, Hint, Status, Detail string
	CanSet                                        bool `json:"can_set"`
}

// secretsEnv is a dashboard over a scanned repo acme-web whose machine map
// has one keychain, one op:// and one env: reference.
func secretsEnv(t *testing.T) (*browserEnv, string, string) {
	t.Helper()
	e := newBrowserEnv(t)
	fakeKeychain(t, map[string]string{"acme-npm-token": canaryKeychain})
	repo := e.readyRepo("acme-web")
	opCalls := fakeOp(t, canaryOp)
	for _, m := range []string{"NPM_TOKEN=keychain:acme-npm-token", "STRIPE_KEY=op://Dev/acme-stripe/test-key", "SHELL_TOKEN=env:ACME_SHELL_TOKEN", "NEW_TOKEN=keychain:acme-new-token"} {
		var out, errBuf strings.Builder
		if code := cli.Run([]string{"secret", "map", repo, m}, &out, &errBuf); code != 0 {
			t.Fatalf("secret map %s: %s", m, errBuf.String())
		}
	}
	return e, repo, opCalls
}

func decodeSecrets(t *testing.T, rec *httptest.ResponseRecorder) map[string]apiSecret {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Secrets []apiSecret `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	m := map[string]apiSecret{}
	for _, s := range body.Secrets {
		m[s.Name] = s
	}
	return m
}

func noValues(t *testing.T, where, body string) {
	t.Helper()
	for _, c := range []string{canaryOp, canaryKeychain} {
		if strings.Contains(body, c) {
			t.Fatalf("%s holds a secret value: %s", where, body)
		}
	}
}

// GET lists names, references and statuses without a store prompt: op://
// stays "not checked" and op never runs; a keychain item answers from its
// existence check.
func TestSecretsGetListsRowsWithoutAPrompt(t *testing.T) {
	e, repo, opCalls := secretsEnv(t)
	rec := e.get("/api/secrets?repo=" + repo)
	rows := decodeSecrets(t, rec)
	noValues(t, "GET /api/secrets", rec.Body.String())
	want := map[string]string{"NPM_TOKEN": "resolves", "NEW_TOKEN": "missing", "STRIPE_KEY": "not checked", "SHELL_TOKEN": "shell only"}
	for name, st := range want {
		if rows[name].Status != st {
			t.Errorf("%s: status %q, want %q", name, rows[name].Status, st)
		}
	}
	if rows["STRIPE_KEY"].Reference != "op://Dev/acme-stripe/test-key" || rows["STRIPE_KEY"].Source != "map" {
		t.Errorf("reference/source: %+v", rows["STRIPE_KEY"])
	}
	if n := countLines(t, opCalls); n != 0 {
		t.Fatalf("GET ran op %d times", n)
	}
}

func TestSecretsGetRefusesAnUnknownRepo(t *testing.T) {
	e, _, _ := secretsEnv(t)
	for _, repo := range []string{"", "/etc", e.root, filepath.Join(e.root, "nope")} {
		if rec := e.get("/api/secrets?repo=" + repo); rec.Code != 403 && rec.Code != 400 {
			t.Errorf("repo %q: status %d", repo, rec.Code)
		}
	}
}

func TestSecretsCheckAuthAndValidation(t *testing.T) {
	e, repo, opCalls := secretsEnv(t)
	body := map[string]string{"repo": repo}
	if rec := e.post("/api/secrets/check", body, reqOpts{noToken: true}); rec.Code != 403 {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := e.post("/api/secrets/check", body, reqOpts{origin: "http://evil.example"}); rec.Code != 403 {
		t.Errorf("bad origin: %d", rec.Code)
	}
	if rec := e.get("/api/secrets/check"); rec.Code != 405 {
		t.Errorf("GET: %d", rec.Code)
	}
	if rec := e.post("/api/secrets/check", map[string]string{"repo": "/etc"}, reqOpts{}); rec.Code != 403 {
		t.Errorf("unscanned repo: %d", rec.Code)
	}
	if n := countLines(t, opCalls); n != 0 {
		t.Fatalf("a refused check ran op %d times", n)
	}
}

// Check reads every reference, updates the statuses and returns no value.
func TestSecretsCheckUpdatesStatuses(t *testing.T) {
	e, repo, opCalls := secretsEnv(t)
	rec := e.post("/api/secrets/check", map[string]string{"repo": repo}, reqOpts{})
	rows := decodeSecrets(t, rec)
	noValues(t, "POST /api/secrets/check", rec.Body.String())
	if rows["STRIPE_KEY"].Status != "resolves" || rows["NPM_TOKEN"].Status != "resolves" || rows["NEW_TOKEN"].Status != "missing" {
		t.Fatalf("after Check: %+v", rows)
	}
	if rows["SHELL_TOKEN"].Status != "shell only" {
		t.Errorf("env: reference: %q", rows["SHELL_TOKEN"].Status)
	}
	if n := countLines(t, opCalls); n != 1 {
		t.Fatalf("op ran %d times, want 1", n)
	}
	// The next GET keeps the checked status and still does not prompt.
	rows = decodeSecrets(t, e.get("/api/secrets?repo="+repo))
	if rows["STRIPE_KEY"].Status != "resolves" {
		t.Errorf("GET after Check: %q", rows["STRIPE_KEY"].Status)
	}
	if n := countLines(t, opCalls); n != 1 {
		t.Fatalf("GET after Check ran op again (%d)", n)
	}
}

func TestSecretsSetValidation(t *testing.T) {
	e, repo, _ := secretsEnv(t)
	post := func(body map[string]string, o reqOpts) int { return e.post("/api/secrets/set", body, o).Code }
	if c := post(map[string]string{"repo": repo, "name": "NPM_TOKEN"}, reqOpts{noToken: true}); c != 403 {
		t.Errorf("no token: %d", c)
	}
	if c := post(map[string]string{"repo": repo, "name": "NPM_TOKEN"}, reqOpts{origin: "http://evil.example"}); c != 403 {
		t.Errorf("bad origin: %d", c)
	}
	if c := post(map[string]string{"repo": "/etc", "name": "NPM_TOKEN"}, reqOpts{}); c != 403 {
		t.Errorf("unknown repo: %d", c)
	}
	if c := post(map[string]string{"repo": repo, "name": "NOT_A_SECRET"}, reqOpts{}); c != 404 {
		t.Errorf("unknown name: %d", c)
	}
	if c := post(map[string]string{"repo": repo, "name": "STRIPE_KEY"}, reqOpts{}); c != 400 {
		t.Errorf("op:// reference: %d (only keychain: takes a value here)", c)
	}
}

// Check on a repo that has an instance also remembers that instance's values
// for redaction, so the log gate (D11) opens after the click and not before.
func TestSecretsCheckOpensTheLogGate(t *testing.T) {
	realHome := os.Getenv("HOME") // compose plugins live under it
	e := newBrowserEnv(t)
	t.Setenv("HOME", realHome)
	repo := scratchRepoAt(t, filepath.Join(e.root, "acme-web"))
	opCalls := fakeOp(t, canaryOp)
	composePath := filepath.Join(repo, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errBuf strings.Builder
	if code := cli.Run([]string{"--root", repo, "--state-dir", e.newDir, "--secret", "tok=op://Dev/acme-stripe/test-key",
		"prepare", "-f", composePath}, &out, &errBuf); code != 0 {
		t.Fatalf("prepare: %s", errBuf.String())
	}
	var prepared struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal([]byte(out.String()), &prepared); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	t.Setenv("DOCKER_FAKE_LOGS", "app-1  | key "+canaryOp+"\n")
	logs := func() *httptest.ResponseRecorder { return e.get("/api/logs?project=" + prepared.Project) }
	rec := logs()
	if rec.Code != 409 || rec.Header().Get("X-Stackfork-Needs") != "secret-check" || strings.Contains(rec.Body.String(), canaryOp) {
		t.Fatalf("before Check: %d %q", rec.Code, rec.Body.String())
	}
	if rec := e.post("/api/secrets/check", map[string]string{"repo": repo}, reqOpts{}); rec.Code != 200 {
		t.Fatalf("check: %d %s", rec.Code, rec.Body.String())
	}
	if rec = logs(); rec.Code != 200 || strings.Contains(rec.Body.String(), canaryOp) || !strings.Contains(rec.Body.String(), "[redacted:tok]") {
		t.Fatalf("after Check: %d %q", rec.Code, rec.Body.String())
	}
	if n := countLines(t, opCalls); n == 0 {
		t.Fatal("Check never read the op:// reference")
	}
}

const typedValue = "typed-canary-7-2"

// stubDialog answers the value dialog; it records what the dialog was asked.
func stubDialog(t *testing.T, available bool, answer string, err error) *[]string {
	t.Helper()
	var asked []string
	oldAvail, oldAsk := dashboard.SecretDialogAvailable, dashboard.AskSecretValue
	dashboard.SecretDialogAvailable = func() bool { return available }
	dashboard.AskSecretValue = func(repo, name, item string) (string, error) {
		asked = append(asked, repo+"|"+name+"|"+item)
		return answer, err
	}
	t.Cleanup(func() { dashboard.SecretDialogAvailable, dashboard.AskSecretValue = oldAvail, oldAsk })
	return &asked
}

// Set value: the dialog's answer reaches the store through its stdin and
// nowhere else - not the HTTP response, not argv, not the process log.
func TestSecretsSetNeverShowsTheValue(t *testing.T) {
	e := newBrowserEnv(t)
	kc := fakeKeychain(t, nil)
	repo := e.readyRepo("acme-web")
	var out, errBuf strings.Builder
	if code := cli.Run([]string{"secret", "map", repo, "NPM_TOKEN=keychain:acme-npm-token"}, &out, &errBuf); code != 0 {
		t.Fatal(errBuf.String())
	}
	asked := stubDialog(t, true, typedValue, nil)
	var logged strings.Builder
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	before := decodeSecrets(t, e.get("/api/secrets?repo="+repo))
	if before["NPM_TOKEN"].Status != "missing" || !before["NPM_TOKEN"].CanSet {
		t.Fatalf("before: %+v", before["NPM_TOKEN"])
	}
	rec := e.post("/api/secrets/set", map[string]string{"repo": repo, "name": "NPM_TOKEN"}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), typedValue) || strings.Contains(logged.String(), typedValue) {
		t.Fatalf("the value leaked: %s / %s", rec.Body.String(), logged.String())
	}
	if len(*asked) != 1 || (*asked)[0] != "acme-web|NPM_TOKEN|acme-npm-token" {
		t.Fatalf("dialog asked: %v", *asked)
	}
	stdin, _ := os.ReadFile(filepath.Join(kc, "stdin.rec"))
	args, _ := os.ReadFile(filepath.Join(kc, "args.rec"))
	if !strings.Contains(string(stdin), typedValue) || strings.Contains(string(args), typedValue) {
		t.Fatalf("value must go through stdin only; stdin=%q args=%q", stdin, args)
	}
	rows := e.get("/api/secrets?repo=" + repo)
	if decodeSecrets(t, rows)["NPM_TOKEN"].Status != "resolves" || strings.Contains(rows.Body.String(), typedValue) {
		t.Fatalf("after: %s", rows.Body.String())
	}
}

func TestSecretsSetCancelledStoresNothing(t *testing.T) {
	e, repo, _ := secretsEnv(t)
	stubDialog(t, true, "", errors.New("cancelled"))
	rec := e.post("/api/secrets/set", map[string]string{"repo": repo, "name": "NEW_TOKEN"}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "nothing stored") {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if decodeSecrets(t, e.get("/api/secrets?repo="+repo))["NEW_TOKEN"].Status != "missing" {
		t.Fatal("a cancelled dialog stored something")
	}
}

// Without a native dialog nothing opens: the row offers the command.
func TestSecretsSetWithoutADialogOpensNothing(t *testing.T) {
	e, repo, _ := secretsEnv(t)
	asked := stubDialog(t, false, typedValue, nil)
	rec := e.post("/api/secrets/set", map[string]string{"repo": repo, "name": "NPM_TOKEN"}, reqOpts{})
	if rec.Code != 501 || len(*asked) != 0 || !strings.Contains(rec.Body.String(), "stackfork secret set acme-npm-token") {
		t.Fatalf("%d %s asked=%v", rec.Code, rec.Body.String(), *asked)
	}
	rows := decodeSecrets(t, e.get("/api/secrets?repo="+repo))
	if rows["NPM_TOKEN"].CanSet {
		t.Error("can_set is true without a dialog")
	}
}

// A forged request (no token) never reaches the dialog.
func TestSecretsSetForgedRequestOpensNoDialog(t *testing.T) {
	e, repo, _ := secretsEnv(t)
	asked := stubDialog(t, true, typedValue, nil)
	for _, o := range []reqOpts{{noToken: true}, {origin: "http://evil.example"}} {
		if rec := e.post("/api/secrets/set", map[string]string{"repo": repo, "name": "NPM_TOKEN"}, o); rec.Code != 403 {
			t.Errorf("%+v: %d", o, rec.Code)
		}
	}
	if len(*asked) != 0 {
		t.Fatalf("dialog opened: %v", *asked)
	}
}
