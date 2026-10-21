package dashboard_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const hostileHint = `paste it <script>alert(1)</script> "quoted" & 'single' {{.}}`

// secretsFragment is the Repos fragment of a repo with declared and mapped
// secrets, and the part of it that is the Secrets section.
func secretsFragment(t *testing.T, e *browserEnv, repo string) (page, section string) {
	t.Helper()
	rec := e.get("/fragments/repos")
	if rec.Code != 200 {
		t.Fatalf("fragment: %d", rec.Code)
	}
	page = rec.Body.String()
	m := regexp.MustCompile(`(?s)<section class="secrets".*?</section>`).FindString(page)
	return page, m
}

func TestSecretsSectionRendersRowsWithoutValuesOrInputs(t *testing.T) {
	e, repo, opCalls := secretsEnv(t)
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified, Secrets: []knowledge.SecretDecl{
		{Name: "NPM_TOKEN", Hint: hostileHint}, {Name: "UNMAPPED_KEY", Hint: "ask the platform team"}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	stubDialog(t, true, "", nil)
	page, section := secretsFragment(t, e, repo)
	if section == "" {
		t.Fatal("no Secrets section in the fragment")
	}
	noValues(t, "fragment", page)
	for _, bad := range []string{"<input", "<textarea", `type="password"`, "<script>alert(1)"} {
		if strings.Contains(section, bad) {
			t.Errorf("section contains %q", bad)
		}
	}
	if !strings.Contains(section, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("hint is not escaped: %s", section)
	}
	for _, want := range []string{
		"NPM_TOKEN", "STRIPE_KEY", "UNMAPPED_KEY", "keychain:acme-npm-token", "op://Dev/acme-stripe/test-key",
		"Resolves", "Not checked", "Shell only", "Missing", `data-secrets-check`, `data-secret-set`,
		`data-key="` + repo + `/secret/NPM_TOKEN"`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q", want)
		}
	}
	// Set value is offered for keychain: rows only; never for op://.
	if n := strings.Count(section, "data-secret-set"); n != 2 { // NPM_TOKEN, NEW_TOKEN
		t.Errorf("data-secret-set appears %d times, want 2", n)
	}
	if n := countLines(t, opCalls); n != 0 {
		t.Fatalf("rendering the page ran op %d times", n)
	}
}

// Without a native dialog the row offers the command and no Set value button.
func TestSecretsSectionOffersCommandsWhenThereIsNoDialog(t *testing.T) {
	e, repo, _ := secretsEnv(t)
	stubDialog(t, false, "", nil)
	_, section := secretsFragment(t, e, repo)
	if strings.Contains(section, "data-secret-set") {
		t.Error("Set value offered without a dialog")
	}
	if !strings.Contains(section, "stackfork secret set acme-npm-token") || !strings.Contains(section, "stackfork secret map") {
		t.Errorf("no copy commands: %s", section)
	}
}

func TestSecretsSectionIsAbsentWithoutNames(t *testing.T) {
	e := newBrowserEnv(t)
	fakeKeychain(t, nil)
	e.readyRepo("acme-api")
	if _, section := secretsFragment(t, e, ""); section != "" {
		t.Fatalf("a repo with no secrets shows a section: %s", section)
	}
}

// A name that fails validation never reaches the page: the config refuses it.
func TestSecretsHostileNameNeverRenders(t *testing.T) {
	e := newBrowserEnv(t)
	fakeKeychain(t, nil)
	repo := e.readyRepo("acme-web")
	var out, errBuf strings.Builder
	if code := cli.Run([]string{"secret", "map", repo, `"><img src=x onerror=alert(1)>=keychain:acme-x`}, &out, &errBuf); code == 0 {
		t.Fatal("map accepted a hostile name")
	}
	page := e.get("/fragments/repos").Body.String()
	if strings.Contains(page, "onerror") {
		t.Fatal("hostile name reached the page")
	}
}

// The live plan list carries the preflight as its first row, so a Start that
// fails at step secrets has a row to turn Failed (add-plan-runs D13).
func TestPlanListHasASecretsRowFirstWhenTheRepoHasSecrets(t *testing.T) {
	e, repo, _ := secretsEnv(t)
	hybridPlan(t, repo, "app")
	frag := e.get("/fragments/repos").Body.String()
	i := strings.Index(frag, `data-step="secrets"`)
	if i < 0 {
		t.Fatalf("no secrets row in the plan list:\n%s", frag)
	}
	if j := strings.Index(frag, `data-step="up"`); j >= 0 && j < i {
		t.Error("the secrets row is not first")
	}
	if !strings.Contains(frag[i:i+400], "resolve secrets") {
		t.Errorf("secrets row text: %s", frag[i:i+400])
	}
}

func TestPlanListHasNoSecretsRowWithoutSecrets(t *testing.T) {
	e := newBrowserEnv(t)
	fakeKeychain(t, nil)
	repo := e.readyRepo("acme-api")
	hybridPlan(t, repo, "app")
	if frag := e.get("/fragments/repos").Body.String(); strings.Contains(frag, `data-step="secrets"`) {
		t.Fatal("a repo with no secrets lists a secrets row")
	}
}

// R4: a folder row names its source and folder, and the unmap command it
// copies targets the folder, not the repository.
func TestSecretsSectionShowsFolderSource(t *testing.T) {
	e, repo, _ := secretsEnv(t)
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified, Secrets: []knowledge.SecretDecl{{Name: "FOLDER_TOK"}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Dir(repo)
	var out, errBuf strings.Builder
	if code := cli.Run([]string{"secret", "map", folder, "FOLDER_TOK=keychain:idv-tok"}, &out, &errBuf); code != 0 {
		t.Fatal(errBuf.String())
	}
	_, section := secretsFragment(t, e, repo)
	if !strings.Contains(section, `title="folder: `+folder+`"`) {
		t.Errorf("no folder source: %s", section)
	}
	if !strings.Contains(section, `data-copy="stackfork secret unmap `+folder+` FOLDER_TOK"`) &&
		!strings.Contains(section, `data-copy="stackfork secret unmap &#39;`+folder) {
		t.Errorf("unmap does not target the folder: %s", section)
	}
}
