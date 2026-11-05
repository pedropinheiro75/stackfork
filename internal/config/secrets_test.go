package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

func writeCfg(t *testing.T, body string) string {
	dir := tempConfig(t)
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Task 3.1: an old config without the key loads as having no secrets.
func TestSecretsMissingKeyIsEmpty(t *testing.T) {
	writeCfg(t, `{"roots":[],"mode":"hybrid"}`)
	c, err := Load()
	if err != nil || len(c.SecretsFor("/repo")) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSecretsLoadValidates(t *testing.T) {
	repo, _ := identity.ResolvePath(t.TempDir()) // a key as the CLI writes it, drive letter included on Windows
	key, _ := json.Marshal(repo)
	good := `{"secrets":{` + string(key) + `:{"NPM_TOKEN":"keychain:acme-npm-token","STRIPE_TEST_KEY":"op://Dev/acme-stripe/test-key","A":"env:X","B":"npmrc:/p/.npmrc#h"}}}`
	writeCfg(t, good)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SecretsFor(repo); len(got) != 4 || got["NPM_TOKEN"] != "keychain:acme-npm-token" {
		t.Fatalf("%v", got)
	}

	const token = "ghp_canaryRawTokenValue1234567890"
	for name, tc := range map[string]struct{ body, mustHave string }{
		"pasted token":  {`{"secrets":{"/code/acme-web":{"NPM_TOKEN":"` + token + `"}}}`, "not a reference"},
		"bad keychain":  {`{"secrets":{"/code/acme-web":{"NPM_TOKEN":"keychain:bad item ` + token + `"}}}`, "NPM_TOKEN"},
		"op with query": {`{"secrets":{"/code/acme-web":{"NPM_TOKEN":"op://Dev/x/y?` + token + `"}}}`, "NPM_TOKEN"},
		"bad name":      {`{"secrets":{"/code/acme-web":{"bad name":"env:X"}}}`, "/code/acme-web"},
		"empty ref":     {`{"secrets":{"/code/acme-web":{"NPM_TOKEN":""}}}`, "NPM_TOKEN"},
	} {
		writeCfg(t, tc.body)
		_, err := Load()
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		msg := err.Error()
		if strings.Contains(msg, token) || strings.Contains(msg, "canary") {
			t.Errorf("%s: error prints the pasted text: %s", name, msg)
		}
		if !strings.Contains(msg, tc.mustHave) || !strings.Contains(msg, "/code/acme-web") {
			t.Errorf("%s: error must name the repository and secret: %s", name, msg)
		}
	}
}

func TestSecretsNonStringValueDoesNotEchoEither(t *testing.T) {
	writeCfg(t, `{"secrets":{"/code/acme-web":{"NPM_TOKEN":12345}}}`)
	if _, err := Load(); err == nil || strings.Contains(err.Error(), "12345") {
		t.Fatalf("%v", err)
	}
}

func TestMapUnmapAndSaveKeepsUnknownKeys(t *testing.T) {
	p := writeCfg(t, `{"roots":[],"future":"keep"}`)
	repo := t.TempDir()
	resolved, err := identity.ResolvePath(repo)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := Load()
	if err := c.Map(repo, "NPM_TOKEN", "keychain:acme-npm-token"); err != nil {
		t.Fatal(err)
	}
	if err := c.Map(repo, "STRIPE_TEST_KEY", "op://Dev/acme-stripe/test-key"); err != nil {
		t.Fatal(err)
	}
	if err := c.Map(repo, "NPM_TOKEN", "keychain:other"); err != nil { // replace
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)
	if string(m["future"]) != `"keep"` || m["secrets"] == nil {
		t.Fatalf("%s", data)
	}
	c2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c2.SecretsFor(repo); got["NPM_TOKEN"] != "keychain:other" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if _, ok := c2.Secrets[resolved]; !ok {
		t.Fatalf("not keyed by the resolved path: %v", c2.Secrets)
	}
	// SecretsFor hands out a copy.
	c2.SecretsFor(repo)["NPM_TOKEN"] = "env:HACK"
	if c2.SecretsFor(repo)["NPM_TOKEN"] != "keychain:other" {
		t.Fatal("SecretsFor exposes the map")
	}

	if err := c2.Unmap(repo, "NPM_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := c2.Unmap(repo, "NPM_TOKEN"); err == nil {
		t.Fatal("unmapping twice succeeded")
	}
	if err := c2.Unmap(repo, "STRIPE_TEST_KEY"); err != nil {
		t.Fatal(err)
	}
	if len(c2.Secrets) != 0 {
		t.Fatalf("empty repo entry kept: %v", c2.Secrets)
	}
	c2.Save()
	data, _ = os.ReadFile(p)
	if strings.Contains(string(data), `"secrets"`) || !strings.Contains(string(data), `"future"`) {
		t.Fatalf("%s", data)
	}
}

func TestMapRefusesBadInputWithoutEcho(t *testing.T) {
	tempConfig(t)
	c, _ := Load()
	repo := t.TempDir()
	if err := c.Map(repo, "bad name", "env:X"); err == nil {
		t.Fatal("bad name")
	}
	err := c.Map(repo, "NPM_TOKEN", "ghp_canaryRawTokenValue")
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Fatalf("%v", err)
	}
	if len(c.Secrets) != 0 {
		t.Fatal("refused entry was kept")
	}
}

// Round 2: a name that is reserved says so, and a config that holds one can
// still be repaired with `secret unmap`.
func TestReservedNameSaysReservedAndUnmapStillWorks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STACKFORK_CONFIG_DIR", dir)
	repo := t.TempDir()
	resolved, _ := identity.ResolvePath(repo)
	body, _ := json.Marshal(map[string]any{"roots": []string{}, "exclude": []string{}, "secrets": map[string]any{resolved: map[string]string{"PATH": "env:X", "NPM_TOKEN": "env:Y"}}})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Load: %v", err)
	}
	if err := (&Config{}).Map(repo, "COMPOSE_FILE", "env:X"); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Map: %v", err)
	}
	c, err := LoadForUnmap()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Unmap(repo, "PATH"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := Load()
	if err != nil || c2.SecretsFor(repo)["NPM_TOKEN"] != "env:Y" || len(c2.SecretsFor(repo)) != 1 {
		t.Fatalf("%v %v", c2, err)
	}
}

func mkdirs(t *testing.T, base string, rel ...string) []string {
	t.Helper()
	var out []string
	for _, r := range rel {
		p := filepath.Join(base, r)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		rp, err := identity.ResolvePath(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rp)
	}
	return out
}

// R2: an ancestor matches by path segments, never by string prefix, and the
// nearest folder wins per name. The repo's own entry is not inherited.
func TestInheritedSecretsForByAncestorSegments(t *testing.T) {
	tempConfig(t)
	base := t.TempDir()
	d := mkdirs(t, base, "acme", "acme/team", "acme/team/app", "acme-other/app")
	rr, team, app, other := d[0], d[1], d[2], d[3]
	c := &Config{Secrets: map[string]map[string]string{
		rr:    {"NPM_TOKEN": "keychain:far", "ONLY_FAR": "env:FAR"},
		team:  {"NPM_TOKEN": "keychain:near"},
		app:   {"NPM_TOKEN": "keychain:own"},
		other: {"X": "env:X"},
	}}
	refs, from := c.InheritedSecretsFor(app)
	if len(refs) != 2 || refs["NPM_TOKEN"] != "keychain:near" || refs["ONLY_FAR"] != "env:FAR" {
		t.Fatalf("%v", refs)
	}
	if from["NPM_TOKEN"] != team || from["ONLY_FAR"] != rr {
		t.Fatalf("%v", from)
	}
	if got := c.SecretsFor(app); len(got) != 1 || got["NPM_TOKEN"] != "keychain:own" {
		t.Fatalf("own: %v", got)
	}
	if r, _ := c.InheritedSecretsFor(filepath.Join(rr + "-other")); len(r) != 0 {
		t.Fatalf("string prefix matched: %v", r)
	}
	if r, _ := c.InheritedSecretsFor(rr); len(r) != 0 {
		t.Fatalf("a key is not its own ancestor: %v", r)
	}
}

func TestMapUnmapOnFolder(t *testing.T) {
	tempConfig(t)
	folder := t.TempDir()
	c, _ := Load()
	if err := c.Map(folder, "NPM_TOKEN", "keychain:acme-npm"); err != nil {
		t.Fatal(err)
	}
	if err := c.Unmap(folder, "NPM_TOKEN"); err != nil || len(c.Secrets) != 0 {
		t.Fatalf("%v %v", err, c.Secrets)
	}
}

// R5: what a folder mapping refuses, and why.
func TestMapFolderRefusals(t *testing.T) {
	tempConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("USERPROFILE", home)
	base := t.TempDir()
	file := filepath.Join(base, "f.txt")
	os.WriteFile(file, []byte("x"), 0o600)
	repo := mkdirs(t, base, "repo")[0]
	os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	sub := mkdirs(t, base, "repo/sub")[0]
	c, _ := Load()
	for name, tc := range map[string]struct{ path, why string }{
		"root":    {string(filepath.Separator), "root"},
		"home":    {home, "home"},
		"file":    {file, "not a directory"},
		"missing": {filepath.Join(base, "nope"), "does not exist"},
		"in repo": {sub, "inside the repository"},
	} {
		err := c.Map(tc.path, "NPM_TOKEN", "keychain:x")
		if err == nil || !strings.Contains(err.Error(), tc.why) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := c.Map(repo, "NPM_TOKEN", "keychain:x"); err != nil {
		t.Fatalf("a repository root is still allowed: %v", err)
	}
	if len(c.Secrets) != 1 {
		t.Fatalf("%v", c.Secrets)
	}
}

// F1: a repository's own entry is never a folder, so a nested clone or a
// submodule (.git as a file) under it does not inherit it.
func TestInheritedSecretsSkipsRepositoryKeys(t *testing.T) {
	tempConfig(t)
	d := mkdirs(t, t.TempDir(), "r/.git", "r/vendor/lib", "r/sub")
	r, nested, sub := d[0][:len(d[0])-len("/.git")], d[1], d[2]
	os.Mkdir(filepath.Join(nested, ".git"), 0o755)
	os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o600)
	c := &Config{Secrets: map[string]map[string]string{r: {"NPM_TOKEN": "keychain:r-own"}}}
	for _, p := range []string{nested, sub} {
		if refs, _ := c.InheritedSecretsFor(p); len(refs) != 0 {
			t.Errorf("%s inherited a repository's own mapping: %v", p, refs)
		}
	}
}

// F2: keys at or above the home directory (or the root) are never folders,
// and a map target there is refused. A home that is a repository (dotfiles)
// does not make the folders under it count as inside a repository.
func TestBroadKeysAreIgnoredAndRefused(t *testing.T) {
	tempConfig(t)
	base, _ := identity.ResolvePath(t.TempDir())
	home := filepath.Join(base, "home")
	app := mkdirs(t, home, ".git", "clients/acme/app")[1]
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("USERPROFILE", home)
	c := &Config{Secrets: map[string]map[string]string{
		home: {"A": "env:A"}, base: {"B": "env:B"}, string(filepath.Separator): {"C": "env:C"},
	}}
	if refs, _ := c.InheritedSecretsFor(app); len(refs) != 0 {
		t.Fatalf("broad keys inherited: %v", refs)
	}
	if err := c.Map(base, "X", "env:X"); err == nil || !strings.Contains(err.Error(), "above your home") {
		t.Errorf("a folder above home: %v", err)
	}
	if err := c.Map(filepath.Join(home, "clients", "acme"), "X", "env:X"); err != nil {
		t.Errorf("a folder under a home that is a repository: %v", err)
	}
}
