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
	good := `{"secrets":{"/code/acme-web":{"NPM_TOKEN":"keychain:acme-npm-token","STRIPE_TEST_KEY":"op://Dev/acme-stripe/test-key","A":"env:X","B":"npmrc:/p/.npmrc#h"}}}`
	writeCfg(t, good)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SecretsFor("/code/acme-web"); len(got) != 4 || got["NPM_TOKEN"] != "keychain:acme-npm-token" {
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
