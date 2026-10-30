package lifecycle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// folderRepo is a repository under a mapped folder; it declares declared.
func folderRepo(t *testing.T, folderRefs map[string]string, declared ...string) (folder, repo string) {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	base, err := identity.ResolvePath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder = filepath.Join(base, "idv")
	repo = filepath.Join(folder, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified}
	for _, n := range declared {
		k.Secrets = append(k.Secrets, knowledge.SecretDecl{Name: n})
	}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	for n, r := range folderRefs {
		if err := cfg.Map(folder, n, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	return folder, repo
}

// R3: a folder reference reaches only a name the repository declares or
// has recorded, and never makes `up` ask for a new prepare.
func TestFolderSecretsOnlyForNamesTheRepoUses(t *testing.T) {
	folder, repo := folderRepo(t, map[string]string{"NPM_TOKEN": "keychain:acme-npm", "OTHER": "keychain:acme-other", "REC": "keychain:acme-rec"}, "NPM_TOKEN")
	refs, from, err := FolderSecrets(repo, map[string]string{"REC": "env:OLD"})
	if err != nil || len(refs) != 2 || refs["NPM_TOKEN"] != "keychain:acme-npm" || refs["REC"] != "keychain:acme-rec" || from["NPM_TOKEN"] != folder {
		t.Fatalf("%v %v %v", refs, from, err)
	}

	// up: a recorded name takes the folder's reference; an undeclared, unrecorded one is ignored.
	info := &state.Manifest{Root: repo, Secrets: map[string]string{"REC": "env:OLD"}}
	mapped, _ := MappedSecrets(repo)
	got, source, unprepared := InstanceSecrets(info, mapped)
	if len(unprepared) != 0 || len(got) != 1 || got["REC"] != "keychain:acme-rec" || source["REC"] != SourceFolder {
		t.Fatalf("%v %v %v", got, source, unprepared)
	}
	// a declared name the manifest lacks cannot be rendered: ignored, not "unprepared"
	if _, ok := got["NPM_TOKEN"]; ok {
		t.Fatal("a name the instance was prepared without was added")
	}
}

func TestOwnMapBeatsFolderBeatsManifest(t *testing.T) {
	_, repo := folderRepo(t, map[string]string{"A": "keychain:folder-a", "B": "keychain:folder-b"}, "A", "B")
	cfg, _ := config.Load()
	if err := cfg.Map(repo, "A", "keychain:own-a"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	info := &state.Manifest{Root: repo, Secrets: map[string]string{"A": "env:M", "B": "env:M"}}
	mapped, _ := MappedSecrets(repo)
	got, source, _ := InstanceSecrets(info, mapped)
	if got["A"] != "keychain:own-a" || source["A"] != SourceMap || got["B"] != "keychain:folder-b" || source["B"] != SourceFolder {
		t.Fatalf("%v %v", got, source)
	}
}

func TestSecretRowsShowFolderSourceAndFrom(t *testing.T) {
	folder, repo := folderRepo(t, map[string]string{"NPM_TOKEN": "keychain:acme-npm", "OTHER": "keychain:acme-other"}, "NPM_TOKEN")
	rows, err := SecretRows(repo, repo, false, nil, nil, t.TempDir(), t.TempDir())
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	if r := rows[0]; r.Name != "NPM_TOKEN" || r.Reference != "keychain:acme-npm" || r.Source != SourceFolder || r.From != folder {
		t.Fatalf("%+v", r)
	}
}

// The preflight line of a folder reference says which folder it came from.
func TestPreflightNamesTheFolder(t *testing.T) {
	t.Setenv("SF_FOLDER_PREFLIGHT", "x")
	folder, repo := folderRepo(t, map[string]string{"TOK": "env:SF_FOLDER_PREFLIGHT"}, "TOK")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"root":"`+repo+`","secrets":{"TOK":"env:OLD"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := InstancePreflight(nil, &out, repo, dir, false); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "(from folder "+folder+")") {
		t.Fatalf("%s", out.String())
	}
}
