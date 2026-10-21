package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func declareSecrets(t *testing.T, repo string, names ...string) {
	t.Helper()
	p := filepath.Join(repo, ".stackfork", "knowledge.json")
	k, err := knowledge.Load(p)
	if err != nil || k == nil {
		k = &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified}
	}
	for _, n := range names {
		k.Secrets = append(k.Secrets, knowledge.SecretDecl{Name: n})
	}
	if err := k.Save(p); err != nil {
		t.Fatal(err)
	}
}

// R4: secret ls shows the folder source and which folder it came from.
func TestSecretLsJSONShowsFolderSourceAndFrom(t *testing.T) {
	installFakeDocker(t)
	root, stateDir, _ := secretRepo(t)
	declareSecrets(t, root, "FOLDER_TOK")
	folder, _ := identity.ResolvePath(filepath.Dir(root))
	mapSecret(t, folder, "FOLDER_TOK", "keychain:idv-tok")
	mapSecret(t, folder, "UNUSED", "keychain:idv-unused")
	out, e, code := runCLI(t, "--state-dir", stateDir, "secret", "ls", root, "--json")
	if code != 0 {
		t.Fatalf("%d %s", code, e)
	}
	var rows []struct{ Name, Reference, Source, From string }
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "FOLDER_TOK" || rows[0].Source != "folder" || rows[0].From != folder {
		t.Fatalf("%s", out)
	}
}

func TestSecretMapFolderAndRefusals(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	folder := filepath.Join(t.TempDir(), "idv")
	os.Mkdir(folder, 0o755)
	out, e, code := runCLI(t, "secret", "map", folder, "NPM_TOKEN=keychain:acme-npm")
	if code != 0 || !strings.Contains(out, "for folder ") || !strings.Contains(out, "applies to the repositories under it that use NPM_TOKEN") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if out, _, _ := runCLI(t, "secret", "unmap", folder, "NPM_TOKEN"); !strings.Contains(out, "unmapped NPM_TOKEN") {
		t.Fatal(out)
	}
	for _, p := range []string{home, string(filepath.Separator), filepath.Join(folder, "missing")} {
		if _, e, code := runCLI(t, "secret", "map", p, "NPM_TOKEN=keychain:x"); code == 0 || e == "" {
			t.Errorf("%s accepted", p)
		}
	}
	cfg, _ := config.Load()
	if len(cfg.Secrets) != 0 {
		t.Fatalf("%v", cfg.Secrets)
	}
}

// R3 end to end: the compose child gets the folder's value for a declared
// name and nothing of a folder name the repository does not use.
func TestStartEnvFolderSecretOnlyForDeclaredName(t *testing.T) {
	const canary, other = "folder-canary-value-77", "unused-canary-value-88"
	root, state := reposEnv(t)
	repo := planRepo(t, root, runSteps)
	declareSecrets(t, repo, "npm_token")
	log := installFakeDocker(t)
	fakeKeychain(t, map[string]string{"acme-npm": canary, "idv-unused": other})
	mapSecret(t, root, "npm_token", "keychain:acme-npm")
	mapSecret(t, root, "UNUSED_TOKEN", "keychain:idv-unused")
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	runCLI(t, "config", "set-mode", repo, "container")
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 || strings.Contains(out+e, canary) || strings.Contains(out+e, other) {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	env := dockerEnvLog(log)
	if !strings.Contains(env, "\nnpm_token="+canary+"\n") {
		t.Fatal("the compose child lacks the folder's value")
	}
	if strings.Contains(env, other) || strings.Contains(env, "UNUSED_TOKEN") {
		t.Fatal("a name the repository does not use reached the compose child")
	}
}
