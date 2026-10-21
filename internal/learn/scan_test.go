package learn

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

func fixture(name string) string {
	return filepath.Join("..", "..", "testdata", "learn", name)
}

func serviceByName(services []knowledge.Service, name string) *knowledge.Service {
	for i := range services {
		if services[i].Name == name {
			return &services[i]
		}
	}
	return nil
}

func hasScript(scripts []knowledge.Script, role, name string) bool {
	for _, s := range scripts {
		if s.Role == role && s.Name == name {
			return true
		}
	}
	return false
}

// Task 13.2 check: unit tests on fixtures for Nuxt/Bun, Node/npm, Yarn/Turbo
// monorepo, and a no-Compose app.
func TestScanNuxtBun(t *testing.T) {
	testsupport.RequireCompose(t)
	k, err := Scan(fixture("nuxt-bun"))
	if err != nil {
		t.Fatal(err)
	}
	if k.Status != knowledge.StatusDraft {
		t.Fatalf("expected draft status, got %q", k.Status)
	}
	if len(k.Facts.ComposeFiles) != 1 || k.Facts.ComposeFiles[0] != "compose.yaml" {
		t.Fatalf("expected [compose.yaml], got %v", k.Facts.ComposeFiles)
	}
	db := serviceByName(k.Facts.Services, "db")
	if db == nil {
		t.Fatal("expected a db service")
	}
	if db.DBEngine != "postgres" || db.DBMajorVersion != "17" {
		t.Fatalf("expected postgres 17, got %q %q", db.DBEngine, db.DBMajorVersion)
	}
	if !db.Healthcheck {
		t.Fatal("expected db healthcheck to be recorded")
	}
	app := serviceByName(k.Facts.Services, "app")
	if app == nil || !app.Build {
		t.Fatalf("expected a building app service, got %+v", app)
	}
	if k.Facts.PackageManager != "bun" || k.Facts.Lockfile != "bun.lockb" {
		t.Fatalf("expected bun/bun.lockb, got %q/%q", k.Facts.PackageManager, k.Facts.Lockfile)
	}
	if k.Facts.RuntimeVersions["bun"] != "1.1.34" {
		t.Fatalf("expected bun runtime version, got %v", k.Facts.RuntimeVersions)
	}
	if !hasScript(k.Facts.Scripts, "dev", "dev") || !hasScript(k.Facts.Scripts, "migrate", "db:migrate") || !hasScript(k.Facts.Scripts, "seed", "db:seed") {
		t.Fatalf("expected dev/migrate/seed scripts, got %+v", k.Facts.Scripts)
	}
	if len(k.Inputs) == 0 {
		t.Fatal("expected recorded inputs")
	}
}

func TestScanNodeNpm(t *testing.T) {
	testsupport.RequireCompose(t)
	k, err := Scan(fixture("node-npm"))
	if err != nil {
		t.Fatal(err)
	}
	if k.Facts.PackageManager != "npm" || k.Facts.Lockfile != "package-lock.json" {
		t.Fatalf("expected npm/package-lock.json, got %q/%q", k.Facts.PackageManager, k.Facts.Lockfile)
	}
	if k.Facts.RuntimeVersions["node"] != "20" {
		t.Fatalf("expected node 20, got %v", k.Facts.RuntimeVersions)
	}
	web := serviceByName(k.Facts.Services, "web")
	if web == nil || !web.Build {
		t.Fatalf("expected a building web service, got %+v", web)
	}
	if !hasScript(k.Facts.Scripts, "dev", "dev") || !hasScript(k.Facts.Scripts, "test", "test") {
		t.Fatalf("expected dev/test scripts, got %+v", k.Facts.Scripts)
	}
}

func TestScanYarnTurboMonorepo(t *testing.T) {
	testsupport.RequireCompose(t)
	k, err := Scan(fixture("yarn-turbo-monorepo"))
	if err != nil {
		t.Fatal(err)
	}
	if k.Facts.PackageManager != "yarn" || k.Facts.Lockfile != "yarn.lock" {
		t.Fatalf("expected yarn/yarn.lock, got %q/%q", k.Facts.PackageManager, k.Facts.Lockfile)
	}
	if !hasScript(k.Facts.Scripts, "dev", "dev") || !hasScript(k.Facts.Scripts, "test", "test") {
		t.Fatalf("expected turbo dev/test scripts, got %+v", k.Facts.Scripts)
	}
	if serviceByName(k.Facts.Services, "web") == nil {
		t.Fatalf("expected a web service, got %+v", k.Facts.Services)
	}
}

func TestScanNoCompose(t *testing.T) {
	// No docker needed: no compose files, so Scan never shells out.
	k, err := Scan(fixture("no-compose"))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Facts.ComposeFiles) != 0 {
		t.Fatalf("expected no compose files, got %v", k.Facts.ComposeFiles)
	}
	if len(k.Facts.Services) != 0 {
		t.Fatalf("expected no services, got %v", k.Facts.Services)
	}
	if k.Facts.PackageManager != "" {
		t.Fatalf("expected no lockfile in this fixture, got %q", k.Facts.PackageManager)
	}
	if !hasScript(k.Facts.Scripts, "dev", "dev") {
		t.Fatalf("expected the dev script to still be recorded, got %+v", k.Facts.Scripts)
	}
}

// Task 13.3 check, exercised through the full scan: the isolation dry run
// reports the fixed container_name from the pg17-fixed-name compose
// fixture, without failing the scan.
func TestScanRecordsIsolationFindings(t *testing.T) {
	testsupport.RequireCompose(t)
	k, err := Scan(filepath.Join("..", "..", "testdata", "pg17-fixed-name"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range k.Refusals {
		if f.Kind == "container_name" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a container_name finding, got %+v", k.Refusals)
	}
}

// A compose file's env_file target commonly doesn't exist in a fresh
// checkout (it's typically gitignored, e.g. `.env`): Scan must record that
// rather than fail (found scanning a real project's fresh clone).
func TestScanToleratesMissingEnvFile(t *testing.T) {
	testsupport.RequireCompose(t)
	k, err := Scan(fixture("missing-env-file"))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Facts.Services) != 1 {
		t.Fatalf("expected the scan to still record services, got %+v", k.Facts.Services)
	}
}

// Task 13.2: WriteKnowledge writes knowledge.json/knowledge.md/.gitignore
// under .stackfork/.
func TestWriteKnowledge(t *testing.T) {
	root := t.TempDir()
	k := &knowledge.Knowledge{Status: knowledge.StatusDraft}
	if err := WriteKnowledge(root, k); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"knowledge.json", "knowledge.md", ".gitignore"} {
		if !fileExists(filepath.Join(root, ".stackfork", name)) {
			t.Fatalf("expected .stackfork/%s to exist", name)
		}
	}
	reloaded, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != knowledge.StatusDraft {
		t.Fatalf("expected draft status, got %q", reloaded.Status)
	}
}

// TestWriteKnowledgeGitignoreExcludesRuntimeEnv covers task 13.5's own
// check directly: a real `git check-ignore` in a fixture repo must exclude
// .stackfork/runtime.env and a real .env, while still tracking
// knowledge.json/knowledge.md (and an .env.example, which isn't a secret).
func TestWriteKnowledgeGitignoreExcludesRuntimeEnv(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := WriteKnowledge(root, &knowledge.Knowledge{Status: knowledge.StatusDraft}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".stackfork/runtime.env", ".stackfork/local.env"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{".stackfork/runtime.env", ".stackfork/local.env"} {
		if err := exec.Command("git", "-C", root, "check-ignore", f).Run(); err != nil {
			t.Fatalf("expected %s to be git-ignored: %v", f, err)
		}
	}
	for _, f := range []string{".stackfork/knowledge.json", ".stackfork/knowledge.md"} {
		if err := exec.Command("git", "-C", root, "check-ignore", f).Run(); err == nil {
			t.Fatalf("expected %s to NOT be git-ignored (it's meant to be committed)", f)
		}
	}
}

func TestScanProposesEntry(t *testing.T) {
	testsupport.RequireCompose(t)
	k, err := Scan(fixture("nuxt-bun"))
	if err != nil {
		t.Fatal(err)
	}
	if e := k.Entry; e == nil || e.Service != "app" || e.Port != 3000 || !e.Proposed || len(k.Links) != 0 {
		t.Fatalf("%+v %+v", k.Entry, k.Links)
	}
}
