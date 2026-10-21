package knowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeK(t *testing.T, secretsJSON string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "knowledge.json")
	body := `{"schema":2,"status":"draft","facts":{},"plan":{}`
	if secretsJSON != "" {
		body += `,"secrets":` + secretsJSON
	}
	body += `}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Task 3.2: names (and optional hints) round-trip; schema stays 2.
func TestSecretsRoundTrip(t *testing.T) {
	p := writeK(t, `[{"name":"NPM_TOKEN","hint":"GitHub Packages read token"},{"name":"STRIPE_TEST_KEY"}]`)
	k, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []SecretDecl{{Name: "NPM_TOKEN", Hint: "GitHub Packages read token"}, {Name: "STRIPE_TEST_KEY"}}
	if !reflect.DeepEqual(k.Secrets, want) {
		t.Fatalf("%+v", k.Secrets)
	}
	out := filepath.Join(t.TempDir(), "k.json")
	if err := k.Save(out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), `"schema": 2`) || strings.Contains(string(data), `"hint": ""`) {
		t.Fatalf("%s", data)
	}
	k2, err := Load(out)
	if err != nil || !reflect.DeepEqual(k2.Secrets, want) {
		t.Fatalf("%+v %v", k2, err)
	}
}

func TestSecretsAbsentIsFine(t *testing.T) {
	k, err := Load(writeK(t, ""))
	if err != nil || len(k.Secrets) != 0 {
		t.Fatalf("%+v %v", k, err)
	}
}

// A reference smuggled into a committed knowledge.json is refused, naming
// the entry and the key, and nothing resolves it.
func TestSecretsLoadRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ json, want string }{
		"ref key":      {`[{"name":"NPM_TOKEN","ref":"op://Private/bank/password"}]`, `"ref"`},
		"value key":    {`[{"name":"NPM_TOKEN","value":"ghp_canary"}]`, `"value"`},
		"unknown key":  {`[{"name":"NPM_TOKEN","other":"x"}]`, `"other"`},
		"bad name":     {`[{"name":"bad name"}]`, "name"},
		"empty name":   {`[{"hint":"x"}]`, "name"},
		"hint control": {`[{"name":"A","hint":"line\nbreak"}]`, "hint"},
		"hint escape":  {`[{"name":"A","hint":"x\u001b[31m"}]`, "hint"},
		"duplicate":    {`[{"name":"A"},{"name":"A"}]`, "twice"},
		"not a list":   {`{"name":"A"}`, "secrets"},
		"name type":    {`[{"name":5}]`, "name"},
		"hint type":    {`[{"name":"A","hint":5}]`, "hint"},
		"entry type":   {`["NPM_TOKEN"]`, "secrets[0]"},
	} {
		_, err := Load(writeK(t, tc.json))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q lacks %q", name, err, tc.want)
		}
		if strings.Contains(err.Error(), "op://") || strings.Contains(err.Error(), "canary") {
			t.Errorf("%s: error echoes a value: %v", name, err)
		}
	}
	// the refused entry is named when its name is valid.
	_, err := Load(writeK(t, `[{"name":"NPM_TOKEN","ref":"op://x/y/z"}]`))
	if err == nil || !strings.Contains(err.Error(), "NPM_TOKEN") {
		t.Fatalf("entry not named: %v", err)
	}
}

func TestSaveRefusesInvalidSecrets(t *testing.T) {
	k := &Knowledge{Secrets: []SecretDecl{{Name: "bad name"}}}
	if err := k.Save(filepath.Join(t.TempDir(), "k.json")); err == nil {
		t.Fatal("saved an invalid name")
	}
	k = &Knowledge{Secrets: []SecretDecl{{Name: "A", Hint: "x\ty"}}}
	if err := k.Save(filepath.Join(t.TempDir(), "k.json")); err == nil {
		t.Fatal("saved a hint with a control character")
	}
}

func TestSecretsUpsertRemove(t *testing.T) {
	k := &Knowledge{}
	k.UpsertSecret("NPM_TOKEN", "h1")
	k.UpsertSecret("B", "")
	k.UpsertSecret("NPM_TOKEN", "h2")
	if len(k.Secrets) != 2 || k.Secrets[0].Hint != "h2" {
		t.Fatalf("%+v", k.Secrets)
	}
	k.UpsertSecret("NPM_TOKEN", "") // an omitted hint keeps the recorded one
	if k.Secrets[0].Hint != "h2" {
		t.Fatalf("%+v", k.Secrets)
	}
	if !k.RemoveSecret("NPM_TOKEN") || k.RemoveSecret("NPM_TOKEN") || len(k.Secrets) != 1 {
		t.Fatalf("%+v", k.Secrets)
	}
}

func TestMarkdownListsSecretNamesOnly(t *testing.T) {
	k := &Knowledge{Secrets: []SecretDecl{{Name: "NPM_TOKEN", Hint: "GitHub Packages read token"}}}
	md := k.Markdown()
	if !strings.Contains(md, "NPM_TOKEN") || !strings.Contains(md, "GitHub Packages read token") {
		t.Fatalf("%s", md)
	}
}

func TestReservedDeclaredNameSaysReserved(t *testing.T) {
	_, err := Load(writeK(t, `[{"name":"DOCKER_HOST"}]`))
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("%v", err)
	}
}
