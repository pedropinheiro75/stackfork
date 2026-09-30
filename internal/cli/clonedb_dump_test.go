package cli

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneDBFromDumpAndFromMutuallyExclusive(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", filepath.Join(root, "compose.yaml"))

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "clone-db",
		"--from", "sf-other-1", "--from-dump", "seed.sql")
	if code == 0 || !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive refusal, got code=%d stderr=%s", code, stderr)
	}
}

func TestCloneDBFromDumpRequiresPreparedStack(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	dump := filepath.Join(t.TempDir(), "seed.sql")
	os.WriteFile(dump, []byte("select 1;"), 0o600)

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "clone-db", "--from-dump", dump)
	if code == 0 || !strings.Contains(stderr, "prepare") {
		t.Fatalf("expected a refusal naming prepare, got code=%d stderr=%s", code, stderr)
	}
}

func TestCloneDBFromDumpRefusesWhenNotRunning(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", filepath.Join(root, "compose.yaml"))
	dump := filepath.Join(t.TempDir(), "seed.sql")
	os.WriteFile(dump, []byte("select 1;"), 0o600)

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "clone-db", "--from-dump", dump)
	if code == 0 || !strings.Contains(stderr, "not running") {
		t.Fatalf("expected a refusal naming it not running, got code=%d stderr=%s", code, stderr)
	}
}

func TestCloneDBFromDumpMissingFileRefuses(t *testing.T) {
	installFakeDocker(t)
	root := initRepo(t)
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(fixtureCompose), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", filepath.Join(root, "compose.yaml"))

	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "clone-db",
		"--from-dump", filepath.Join(t.TempDir(), "does-not-exist.sql"))
	if code == 0 || stderr == "" {
		t.Fatalf("expected a refusal for a missing dump file, got code=%d stderr=%s", code, stderr)
	}
}

// TestOpenDumpContentDetectsFormatsWithoutTrustingExtension: magic-byte
// sniffing, not the filename, decides plain-SQL vs Postgres custom-format
// - and .gz transparently decompresses either way, with nothing ever
// written to a temp file.
func TestOpenDumpContentDetectsFormatsWithoutTrustingExtension(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "seed.sql")
	os.WriteFile(plain, []byte("select 1;\n"), 0o600)
	content, isCustom, cleanup, err := openDumpContent(plain)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if isCustom {
		t.Error("plain SQL misdetected as custom-format")
	}
	got, _ := io.ReadAll(content)
	if string(got) != "select 1;\n" {
		t.Errorf("plain content mismatch: %q", got)
	}

	custom := filepath.Join(dir, "seed.dump")
	os.WriteFile(custom, append([]byte(pgCustomFormatMagic), []byte("...binary...")...), 0o600)
	_, isCustom, cleanup2, err := openDumpContent(custom)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup2()
	if !isCustom {
		t.Error("PGDMP-prefixed content not detected as custom-format")
	}

	gz := filepath.Join(dir, "seed.sql.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte("insert into widgets values (1);\n"))
	gw.Close()
	os.WriteFile(gz, buf.Bytes(), 0o600)
	content, isCustom, cleanup3, err := openDumpContent(gz)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup3()
	if isCustom {
		t.Error("gzipped plain SQL misdetected as custom-format")
	}
	got, _ = io.ReadAll(content)
	if string(got) != "insert into widgets values (1);\n" {
		t.Errorf("decompressed content mismatch: %q", got)
	}
}

func TestResolveDBServiceAutoDetectsSingleCandidate(t *testing.T) {
	model := map[string]interface{}{"services": map[string]interface{}{
		"db":    map[string]interface{}{"image": "postgres:17"},
		"cache": map[string]interface{}{"image": "redis:7"},
	}}
	name, kind, err := resolveDBService(model, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "db" || kind != "postgres" {
		t.Errorf("got name=%q kind=%q", name, kind)
	}
}

func TestResolveDBServiceAmbiguousRefusesClearly(t *testing.T) {
	model := map[string]interface{}{"services": map[string]interface{}{
		"db1": map[string]interface{}{"image": "postgres:17"},
		"db2": map[string]interface{}{"image": "mysql:8"},
	}}
	_, _, err := resolveDBService(model, "")
	if err == nil || !strings.Contains(err.Error(), "db1") || !strings.Contains(err.Error(), "db2") {
		t.Fatalf("expected a refusal naming both candidates, got %v", err)
	}
}

func TestResolveDBServiceNoneRefusesClearly(t *testing.T) {
	model := map[string]interface{}{"services": map[string]interface{}{
		"cache": map[string]interface{}{"image": "redis:7"},
	}}
	_, _, err := resolveDBService(model, "")
	if err == nil || !strings.Contains(err.Error(), "--service") {
		t.Fatalf("expected a refusal suggesting --service, got %v", err)
	}
}

func TestResolveDBNameDefaultsForPostgresRefusesForMySQL(t *testing.T) {
	pgRaw := map[string]interface{}{"environment": map[string]interface{}{"POSTGRES_USER": "appuser"}}
	name, err := resolveDBName("postgres", pgRaw, "")
	if err != nil || name != "appuser" {
		t.Fatalf("expected the postgres user as the default db name, got %q err=%v", name, err)
	}

	mysqlRaw := map[string]interface{}{"environment": map[string]interface{}{}}
	_, err = resolveDBName("mysql", mysqlRaw, "")
	if err == nil || !strings.Contains(err.Error(), "--database") {
		t.Fatalf("expected a refusal suggesting --database for mysql with no MYSQL_DATABASE, got %v", err)
	}

	name, err = resolveDBName("mysql", mysqlRaw, "explicit")
	if err != nil || name != "explicit" {
		t.Fatalf("expected --database to win, got %q err=%v", name, err)
	}
}
