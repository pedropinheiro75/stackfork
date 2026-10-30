package dashboard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
)

const valueLike = "hunter2-value-canary-9"

type mapBody struct {
	Scope     string `json:"scope"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

func mapped(t *testing.T, path string) map[string]string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.SecretsFor(path)
}

func mapEnv(t *testing.T) (*browserEnv, string) {
	t.Helper()
	e := newBrowserEnv(t)
	fakeKeychain(t, nil)
	return e, e.readyRepo("acme-web")
}

func TestSecretsMapRepoScope(t *testing.T) {
	e, repo := mapEnv(t)
	stubDialog(t, true, "", nil)
	rec := e.post("/api/secrets/map", mapBody{"repo", repo, "API_TOKEN", "keychain:acme-api-token"}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("map: %d %s", rec.Code, rec.Body.String())
	}
	if got := mapped(t, repo)["API_TOKEN"]; got != "keychain:acme-api-token" {
		t.Fatalf("config holds %q", got)
	}
	// The item does not exist yet: the page is told to offer Set value.
	if !strings.Contains(rec.Body.String(), `"missing":true`) || !strings.Contains(rec.Body.String(), `"can_set":true`) {
		t.Errorf("no Set value offer: %s", rec.Body.String())
	}
	rec = e.post("/api/secrets/map", mapBody{"repo", repo, "OP_KEY", "op://Dev/acme/key"}, reqOpts{})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"missing":true`) {
		t.Errorf("op:// reference: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSecretsMapFolderScope(t *testing.T) {
	e, repo := mapEnv(t)
	folder := filepath.Dir(repo)
	rec := e.post("/api/secrets/map", mapBody{"folder", folder, "FOLDER_TOK", "keychain:acme-tok"}, reqOpts{})
	if rec.Code != 200 {
		t.Fatalf("map: %d %s", rec.Code, rec.Body.String())
	}
	if got := mapped(t, folder)["FOLDER_TOK"]; got != "keychain:acme-tok" {
		t.Fatalf("config holds %q", got)
	}
}

func TestSecretsMapRefusals(t *testing.T) {
	e, repo := mapEnv(t)
	home, _ := os.UserHomeDir()
	for _, c := range []struct {
		why  string
		body mapBody
		code int
		in   string // the refusal says this
	}{
		{"bad name", mapBody{"repo", repo, "lower-case", "keychain:x"}, 400, ""},
		{"value-like reference", mapBody{"repo", repo, "API_TOKEN", valueLike}, 400, "not a reference"},
		{"folder at home", mapBody{"folder", home, "API_TOKEN", "keychain:x"}, 400, "home"},
		{"folder at root", mapBody{"folder", "/", "API_TOKEN", "keychain:x"}, 400, "root"},
		{"missing folder", mapBody{"folder", filepath.Join(e.root, "nope"), "API_TOKEN", "keychain:x"}, 400, "does not exist"},
		{"inside a repo", mapBody{"folder", filepath.Join(repo, ".stackfork"), "API_TOKEN", "keychain:x"}, 400, "inside the repository"},
		{"repo scope, not a scanned repo", mapBody{"repo", "/etc", "API_TOKEN", "keychain:x"}, 403, ""},
		{"unknown scope", mapBody{"nowhere", repo, "API_TOKEN", "keychain:x"}, 400, ""},
	} {
		rec := e.post("/api/secrets/map", c.body, reqOpts{})
		if rec.Code != c.code || strings.Contains(rec.Body.String(), valueLike) || !strings.Contains(rec.Body.String(), c.in) {
			t.Errorf("%s: %d %s", c.why, rec.Code, rec.Body.String())
		}
	}
	if m := mapped(t, repo); len(m) != 0 {
		t.Errorf("a refusal wrote %v", m)
	}
}

func TestSecretsMapAndUnmapNeedTokenAndOrigin(t *testing.T) {
	e, repo := mapEnv(t)
	mapTestSecret(t, repo, "KEEP_ME", "keychain:keep")
	for _, p := range []struct {
		path string
		body any
	}{
		{"/api/secrets/map", mapBody{"repo", repo, "API_TOKEN", "keychain:x"}},
		{"/api/secrets/unmap", map[string]string{"path": repo, "name": "KEEP_ME"}},
	} {
		for _, o := range []reqOpts{{noToken: true}, {origin: "http://evil.example"}} {
			if rec := e.post(p.path, p.body, o); rec.Code != 403 {
				t.Errorf("%s %+v: %d", p.path, o, rec.Code)
			}
		}
		if rec := e.get(p.path); rec.Code != 405 {
			t.Errorf("GET %s: %d", p.path, rec.Code)
		}
	}
	if m := mapped(t, repo); len(m) != 1 || m["KEEP_ME"] == "" {
		t.Fatalf("a forged call changed the map: %v", m)
	}
}

func TestSecretsUnmap(t *testing.T) {
	e, repo := mapEnv(t)
	folder := filepath.Dir(repo)
	mapTestSecret(t, repo, "API_TOKEN", "keychain:a")
	mapTestSecret(t, folder, "FOLDER_TOK", "keychain:b")
	for _, c := range []struct{ path, name string }{{repo, "API_TOKEN"}, {folder, "FOLDER_TOK"}} {
		if rec := e.post("/api/secrets/unmap", map[string]string{"path": c.path, "name": c.name}, reqOpts{}); rec.Code != 200 {
			t.Fatalf("unmap %s: %d %s", c.name, rec.Code, rec.Body.String())
		}
		if len(mapped(t, c.path)) != 0 {
			t.Errorf("%s still mapped", c.name)
		}
	}
	if rec := e.post("/api/secrets/unmap", map[string]string{"path": repo, "name": "API_TOKEN"}, reqOpts{}); rec.Code != 404 {
		t.Errorf("unmapping what is not mapped: %d", rec.Code)
	}
}

// Set value works for a folder mapping too: the dialog names the folder.
func TestSecretsSetForAFolderMapping(t *testing.T) {
	e, repo := mapEnv(t)
	folder := filepath.Dir(repo)
	mapTestSecret(t, folder, "FOLDER_TOK", "keychain:acme-tok")
	asked := stubDialog(t, true, typedValue, nil)
	rec := e.post("/api/secrets/set", map[string]string{"repo": folder, "name": "FOLDER_TOK"}, reqOpts{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) || strings.Contains(rec.Body.String(), typedValue) {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if len(*asked) != 1 || (*asked)[0] != filepath.Base(folder)+"|FOLDER_TOK|acme-tok" {
		t.Fatalf("dialog asked: %v", *asked)
	}
}
