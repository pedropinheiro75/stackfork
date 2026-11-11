package cli

import (
	"encoding/json"
	"os"
	"os/exec"
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
	if err := verifiedFixture(k).Save(p); err != nil {
		t.Fatal(err)
	}
}

// R4: secret ls shows the folder source and which folder it came from.
func TestSecretLsJSONShowsFolderSourceAndFrom(t *testing.T) {
	installFakeDocker(t)
	root, stateDir, _ := secretRepo(t)
	declareSecrets(t, root, "FOLDER_TOK")
	folder, _ := identity.ResolvePath(filepath.Dir(root))
	mapSecret(t, folder, "FOLDER_TOK", "keychain:acme-tok")
	mapSecret(t, folder, "UNUSED", "keychain:acme-unused")
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
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("USERPROFILE", home)
	folder := filepath.Join(t.TempDir(), "acme")
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
	fakeKeychain(t, map[string]string{"acme-npm": canary, "acme-unused": other})
	mapSecret(t, root, "npm_token", "keychain:acme-npm")
	mapSecret(t, root, "UNUSED_TOKEN", "keychain:acme-unused")
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

// F3: declaring a name and mapping it on the folder after the instance was
// prepared makes the next start-env prepare again, so the value arrives.
func TestStartEnvFolderNameDeclaredLaterRePrepares(t *testing.T) {
	const canary = "late-folder-canary-55"
	root, state := reposEnv(t)
	repo := planRepo(t, root, runSteps)
	log := installFakeDocker(t)
	fakeKeychain(t, map[string]string{"acme-late": canary})
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	runCLI(t, "config", "set-mode", repo, "container")
	if _, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatal(e)
	}
	if strings.Contains(dockerEnvLog(log), canary) {
		t.Fatal("canary before the mapping")
	}
	declareSecrets(t, repo, "npm_token")
	mapSecret(t, root, "npm_token", "keychain:acme-late")
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 || strings.Contains(out+e, canary) {
		t.Fatalf("%d\n%s\n%s", code, out, e)
	}
	if !strings.Contains(dockerEnvLog(log), "\nnpm_token="+canary+"\n") {
		t.Fatal("the second start-env did not re-prepare with the folder's name")
	}
}

// F4: a sibling's store reference for a name the folder supplies gets no
// "map it" note.
func TestStartEnvNoSiblingNoteForFolderName(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	installFakeDocker(t)
	whereOut, _, _ := runCLI(t, append(base, "--root", sib, "where")...)
	var w struct{ State string }
	json.Unmarshal([]byte(whereOut), &w)
	mp := filepath.Join(w.State, "manifest.json")
	raw, _ := os.ReadFile(mp)
	var man map[string]any
	json.Unmarshal(raw, &man)
	man["secrets"] = map[string]string{"tok": "keychain:sibling-item-canary"}
	raw, _ = json.Marshal(man)
	os.WriteFile(mp, raw, 0o600)
	declareSecrets(t, repo, "tok")
	mapSecret(t, filepath.Dir(repo), "tok", "env:SF_FOLDER_NOTE_UNSET")
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	ad := filepath.Join(nw, ".stackfork")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "app.compose.yaml"), []byte("services:\n  app:\n    image: alpine:3.20\n"), 0o644)
	out, e, _ := runCLI(t, append(base, "start-env", nw)...)
	if strings.Contains(out+e, "note: sibling's tok") {
		t.Fatalf("the note names a name the folder supplies: %s %s", out, e)
	}
}

// At prepare the flag beats the repository's map, which beats the folder's.
func TestPrepareFlagBeatsMapBeatsFolder(t *testing.T) {
	installFakeDocker(t)
	root, stateDir, inst := secretRepo(t)
	declareSecrets(t, root, "A")
	composePath := filepath.Join(root, "compose.yaml")
	mapSecret(t, filepath.Dir(root), "A", "env:SF_TEST_FOLDER")
	prepareWith(t, root, stateDir, composePath)
	if got := manifestRefs(t, inst)["A"]; got != "env:SF_TEST_FOLDER" {
		t.Fatalf("folder level: %q", got)
	}
	mapSecret(t, root, "A", "env:SF_TEST_OWN")
	prepareWith(t, root, stateDir, composePath)
	if got := manifestRefs(t, inst)["A"]; got != "env:SF_TEST_OWN" {
		t.Fatalf("map must beat the folder: %q", got)
	}
	prepareWith(t, root, stateDir, composePath, "A=env:SF_TEST_FLAG")
	if got := manifestRefs(t, inst)["A"]; got != "env:SF_TEST_FLAG" {
		t.Fatalf("flag must beat the map: %q", got)
	}
}
