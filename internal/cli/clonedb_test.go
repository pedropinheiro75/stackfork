package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// writeCloneDBFixture writes one instance's manifest.json + compose.json
// directly (no prepare/no Docker), with Files set to a path relative to
// root that's consistent across different roots ("compose.yaml" directly
// under root) - unlike writeSyntheticLegacyInstance (whose Files point
// into the state dir itself, unique per project hash), so two fixtures
// built from the same relative filename correctly satisfy
// sameProjectFiles.
func writeCloneDBFixture(t *testing.T, base, project, root, branch, composeJSON string) {
	t.Helper()
	root, err := identity.ResolvePath(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "compose.json")
	if err := os.WriteFile(configPath, []byte(composeJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := state.SHA256File(configPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := &state.Manifest{
		Project: project, Root: root, Branch: branch, Hostname: project + ".localhost",
		Files: []string{filepath.Join(root, "compose.yaml")}, Inputs: map[string]string{}, ConfigSHA256: hash,
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
}

const cloneDBFixtureCompose = `{"services":{"db":{"image":"postgres:17"}},"volumes":{"pgdata":{"name":"pgdata-vol"}}}`

// TestCloneDBRefusesRunningSourceWithoutAllowRunning covers design.md D8:
// a running source must be refused (a copy of a running Postgres is not
// consistent) unless --allow-running is passed - checked before ever
// touching Docker volumes.
func TestCloneDBRefusesRunningSourceWithoutAllowRunning(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	targetRoot := filepath.Join(t.TempDir(), "target-checkout")
	srcRoot := filepath.Join(t.TempDir(), "src-checkout")
	writeCloneDBFixture(t, base, "sf-target-1", targetRoot, "main", cloneDBFixtureCompose)
	writeCloneDBFixture(t, base, "sf-source-1", srcRoot, "main", cloneDBFixtureCompose)
	t.Setenv("DOCKER_FAKE_PS", gcPSLine("sf-source-1", "running")+"\n")

	var out bytes.Buffer
	code, err := cmdCloneDB(&out, []string{"--from", "sf-source-1"}, targetRoot, "sf-target-1", filepath.Join(base, "sf-target-1"), base, t.TempDir())
	if code != 0 || err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

// TestCloneDBRefusesDifferentProject covers the same-project-by-relative-
// path guard.
func TestCloneDBRefusesDifferentProject(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	targetRoot := filepath.Join(t.TempDir(), "target-checkout")
	srcRoot := filepath.Join(t.TempDir(), "src-checkout")
	writeCloneDBFixture(t, base, "sf-target-2", targetRoot, "main", cloneDBFixtureCompose)
	// A different relative Files entry (its own manifest, not via the
	// shared helper) - a different project's Compose file.
	root, _ := identity.ResolvePath(srcRoot)
	dir := filepath.Join(base, "sf-source-2")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "compose.json"), []byte(cloneDBFixtureCompose), 0o600)
	hash, _ := state.SHA256File(filepath.Join(dir, "compose.json"))
	state.WritePrivate(filepath.Join(dir, "manifest.json"), &state.Manifest{
		Project: "sf-source-2", Root: root, Branch: "main", Hostname: "sf-source-2.localhost",
		Files: []string{filepath.Join(root, "some-other-file.yaml")}, Inputs: map[string]string{}, ConfigSHA256: hash,
	})
	t.Setenv("DOCKER_FAKE_PS", "")

	var out bytes.Buffer
	code, err := cmdCloneDB(&out, []string{"--from", "sf-source-2"}, targetRoot, "sf-target-2", filepath.Join(base, "sf-target-2"), base, t.TempDir())
	if code != 0 || err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestSameProjectFilesMatchesByRelativePath(t *testing.T) {
	if !sameProjectFiles(
		"/checkouts/repo-branchA", []string{"/checkouts/repo-branchA/docker-compose.yml"},
		"/checkouts/repo-branchB", []string{"/checkouts/repo-branchB/docker-compose.yml"},
	) {
		t.Fatal("expected the same repo-relative file to match across different roots")
	}
}

func TestSameProjectFilesRejectsDifferentProject(t *testing.T) {
	if sameProjectFiles(
		"/checkouts/repo-a", []string{"/checkouts/repo-a/docker-compose.yml"},
		"/checkouts/repo-b", []string{"/checkouts/repo-b/compose.yaml"},
	) {
		t.Fatal("expected a different relative filename to refuse")
	}
}

func TestSameProjectFilesRejectsEmpty(t *testing.T) {
	if sameProjectFiles("/a", nil, "/b", nil) {
		t.Fatal("expected no files at all to refuse, not match")
	}
}

func TestDefaultDataVolumeFromLabel(t *testing.T) {
	model := map[string]interface{}{
		"services": map[string]interface{}{
			"db": map[string]interface{}{
				"labels": map[string]interface{}{"io.stackfork.data": "mydata"},
			},
		},
	}
	name, err := defaultDataVolume(model)
	if err != nil || name != "mydata" {
		t.Fatalf("name=%q err=%v", name, err)
	}
}

func TestDefaultDataVolumeFromKnownPath(t *testing.T) {
	model := map[string]interface{}{
		"services": map[string]interface{}{
			"db": map[string]interface{}{
				"volumes": []interface{}{
					map[string]interface{}{"source": "pgdata", "target": "/var/lib/postgresql/data"},
				},
			},
		},
	}
	name, err := defaultDataVolume(model)
	if err != nil || name != "pgdata" {
		t.Fatalf("name=%q err=%v", name, err)
	}
}

func TestDefaultDataVolumeRefusesAmbiguous(t *testing.T) {
	model := map[string]interface{}{
		"services": map[string]interface{}{
			"db":    map[string]interface{}{"volumes": []interface{}{map[string]interface{}{"source": "pgdata", "target": "/var/lib/postgresql/data"}}},
			"other": map[string]interface{}{"volumes": []interface{}{map[string]interface{}{"source": "mysqldata", "target": "/var/lib/mysql"}}},
		},
	}
	if _, err := defaultDataVolume(model); err == nil {
		t.Fatal("expected refusal for two candidate data volumes")
	}
}

func TestDefaultDataVolumeRefusesNone(t *testing.T) {
	model := map[string]interface{}{"services": map[string]interface{}{
		"web": map[string]interface{}{"volumes": []interface{}{map[string]interface{}{"source": "web_modules", "target": "/app/node_modules"}}},
	}}
	if _, err := defaultDataVolume(model); err == nil {
		t.Fatal("expected refusal with no known-path candidate")
	}
}

func TestVolumeDockerNameReadsNormalizedName(t *testing.T) {
	model := map[string]interface{}{
		"volumes": map[string]interface{}{
			"pgdata": map[string]interface{}{"name": "sf-myproj-abc123_pgdata"},
		},
	}
	name, ok := volumeDockerName(model, "pgdata")
	if !ok || name != "sf-myproj-abc123_pgdata" {
		t.Fatalf("name=%q ok=%v", name, ok)
	}
}

func TestVolumeDockerNameMissing(t *testing.T) {
	model := map[string]interface{}{"volumes": map[string]interface{}{}}
	if _, ok := volumeDockerName(model, "pgdata"); ok {
		t.Fatal("expected not-found for a volume that isn't declared")
	}
}
