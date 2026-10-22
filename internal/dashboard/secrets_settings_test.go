package dashboard_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func settingsSecrets(t *testing.T, e *browserEnv) string {
	t.Helper()
	page := e.get("/fragments/settings").Body.String()
	i := strings.Index(page, `aria-labelledby="set-secrets"`)
	if i < 0 {
		t.Fatalf("no Secrets section in Settings: %s", page)
	}
	j := strings.Index(page[i:], "</section>")
	return page[i : i+j]
}

func TestSettingsListsRepoAndFolderMappings(t *testing.T) {
	e, repo := mapEnv(t)
	folder := filepath.Dir(repo)
	mapTestSecret(t, repo, "API_TOKEN", "keychain:acme-api-token")
	mapTestSecret(t, folder, "FOLDER_TOK", "op://Dev/acme/tok")
	mapTestSecret(t, repo, "SHELL_TOK", "env:ACME_SHELL")
	stubDialog(t, true, "", nil)
	sec := settingsSecrets(t, e)
	for _, want := range []string{
		"API_TOKEN", "FOLDER_TOK", "keychain:acme-api-token", "op://Dev/acme/tok", repo, folder,
		"scope-chip", "Missing", "Not checked", "Shell only",
		`data-secret-unmap`, `data-secret-add`, `data-secret-set data-repo="` + repo + `" data-name="API_TOKEN"`,
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("section lacks %q:\n%s", want, sec)
		}
	}
	if n := strings.Count(sec, `class="scope-chip"`); n != 1 {
		t.Errorf("%d folder chips, want 1", n)
	}
	if n := strings.Count(sec, "data-secret-set"); n != 1 { // only the keychain: ref takes a value
		t.Errorf("%d Set value buttons, want 1", n)
	}
	for _, bad := range []string{"<input", "<textarea", `type="password"`} {
		if strings.Contains(sec, bad) {
			t.Errorf("section contains %q", bad)
		}
	}
}

func TestSettingsSecretsEmptyState(t *testing.T) {
	e, _ := mapEnv(t)
	sec := settingsSecrets(t, e)
	if !strings.Contains(sec, "data-secret-add") || strings.Contains(sec, "secret-row") {
		t.Errorf("empty state: %s", sec)
	}
	if !strings.Contains(sec, "No secrets mapped") {
		t.Errorf("no empty line: %s", sec)
	}
}

func TestReposPanelAlwaysShowsASecretsSection(t *testing.T) {
	e := newBrowserEnv(t)
	fakeKeychain(t, nil)
	repo := e.readyRepo("acme-api")
	_, section := secretsFragment(t, e, repo)
	if section == "" {
		t.Fatal("a repo without secrets shows no section")
	}
	for _, want := range []string{"No secrets", "data-secret-add", `data-repo="` + repo + `"`} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q: %s", want, section)
		}
	}
	if strings.Contains(section, "data-secrets-check") {
		t.Error("Check offered with nothing to check")
	}
}
