package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// seedKnowledge writes a minimal draft knowledge.json, so the record tests
// need no Compose scan.
func seedKnowledge(t *testing.T) (root, path string) {
	t.Helper()
	root = initRepo(t)
	path = filepath.Join(root, ".stackfork", "knowledge.json")
	k := &knowledge.Knowledge{Status: knowledge.StatusDraft}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestLearnRecordSecretRequiresPriorScan(t *testing.T) {
	root := initBareRepo(t)
	_, stderr, code := runCLI(t, "--root", root, "learn", "record", "secret", "NPM_TOKEN")
	if code == 0 || !strings.Contains(stderr, "run `stackfork learn` first") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

func TestLearnRecordSecretAddUpdateRemove(t *testing.T) {
	root, path := seedKnowledge(t)

	out, stderr, code := runCLI(t, "--root", root, "learn", "record", "secret", "NPM_TOKEN", "--hint", "GitHub Packages read token")
	if code != 0 {
		t.Fatalf("record failed: %s", stderr)
	}
	if !strings.Contains(out, `"recorded": "secret"`) || !strings.Contains(out, "NPM_TOKEN") {
		t.Fatalf("output: %s", out)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "secret", "STRIPE_TEST_KEY"); code != 0 {
		t.Fatalf("second record failed: %s", stderr)
	}
	k, err := knowledge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []knowledge.SecretDecl{{Name: "NPM_TOKEN", Hint: "GitHub Packages read token"}, {Name: "STRIPE_TEST_KEY"}}
	if !reflect.DeepEqual(k.Secrets, want) {
		t.Fatalf("%+v", k.Secrets)
	}

	// flags may come before the name too; an omitted hint keeps the old one.
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "secret", "--hint", "new hint", "STRIPE_TEST_KEY"); code != 0 {
		t.Fatalf("update failed: %s", stderr)
	}
	runCLI(t, "--root", root, "learn", "record", "secret", "NPM_TOKEN")
	k, _ = knowledge.Load(path)
	if k.Secrets[0].Hint != "GitHub Packages read token" || k.Secrets[1].Hint != "new hint" {
		t.Fatalf("%+v", k.Secrets)
	}

	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "secret", "--remove", "NPM_TOKEN"); code != 0 {
		t.Fatalf("remove failed: %s", stderr)
	}
	k, _ = knowledge.Load(path)
	if len(k.Secrets) != 1 || k.Secrets[0].Name != "STRIPE_TEST_KEY" {
		t.Fatalf("%+v", k.Secrets)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "secret", "--remove", "NPM_TOKEN"); code == 0 || !strings.Contains(stderr, "NPM_TOKEN") {
		t.Fatalf("removing an undeclared name: code=%d %s", code, stderr)
	}
}

func TestLearnRecordSecretRefusals(t *testing.T) {
	root, path := seedKnowledge(t)
	before, _ := os.ReadFile(path)
	for name, args := range map[string][]string{
		"no name":      {"record", "secret"},
		"bad name":     {"record", "secret", "ghp_canary token"},
		"hint control": {"record", "secret", "NPM_TOKEN", "--hint", "a\nb"},
		"ref flag":     {"record", "secret", "NPM_TOKEN", "--ref", "op://Private/bank/password"},
		"two names":    {"record", "secret", "A", "B"},
		"remove+hint":  {"record", "secret", "--remove", "A", "--hint", "x"},
	} {
		_, stderr, code := runCLI(t, append([]string{"--root", root, "learn"}, args...)...)
		if code == 0 {
			t.Errorf("%s: accepted", name)
		}
		if strings.Contains(stderr, "canary") {
			t.Errorf("%s: stderr echoes the name: %s", name, stderr)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a refused record changed knowledge.json")
	}
}

func TestLearnSecretHelp(t *testing.T) {
	root, _ := seedKnowledge(t)
	out, _, code := runCLI(t, "--root", root, "learn", "record", "secret", "--help")
	if code != 0 || !strings.Contains(out, "--hint") || !strings.Contains(out, "--remove") {
		t.Fatalf("code=%d %s", code, out)
	}
}

// A re-scan must not drop the declared names.
func TestLearnRescanKeepsDeclaredSecrets(t *testing.T) {
	root, path := seedKnowledge(t)
	runCLI(t, "--root", root, "learn", "record", "secret", "NPM_TOKEN", "--hint", "h")
	fresh := &knowledge.Knowledge{Status: knowledge.StatusDraft}
	if _, resumed := mergeKnowledgeFile(root, path, fresh); !resumed {
		t.Fatal("not resumed")
	}
	if len(fresh.Secrets) != 1 || fresh.Secrets[0].Name != "NPM_TOKEN" {
		t.Fatalf("%+v", fresh.Secrets)
	}
}
