package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const envFixtureCompose = `services:
  web:
    image: nginx
    environment:
      ENABLE_GUEST_ACCESS: "false"
  db:
    image: postgres:17
`

// envFixture prepares an instance with services web and db and returns what
// the env override tests need.
func envFixture(t *testing.T) (log, root, stateDir, project, instanceDir string) {
	t.Helper()
	log = installFakeDocker(t)
	root = initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte(envFixtureCompose), 0o600)
	stateDir = filepath.Join(t.TempDir(), "state")
	m := preparedManifest(t, root, stateDir, "", "-f", composePath)
	realState, _ := filepath.EvalSymlinks(stateDir)
	envBranch = m.Branch
	return log, m.Root, realState, m.Project, filepath.Join(realState, m.Project)
}

// envBranch is the branch of the last envFixture.
var envBranch string

func writeOverrides(t *testing.T, dir string, kv ...string) {
	t.Helper()
	o, _ := state.ReadEnvOverrides(dir)
	for i := 0; i+2 < len(kv); i += 3 {
		if err := o.Set(kv[i], kv[i+1], kv[i+2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.Write(dir); err != nil {
		t.Fatal(err)
	}
}

func TestDerivedOverridesFileAddedAfterSnapshot(t *testing.T) {
	log, root, stateDir, _, dir := envFixture(t)
	snap, _ := os.ReadFile(filepath.Join(dir, "compose.json"))
	m0, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	writeOverrides(t, dir, "web", "ENABLE_GUEST_ACCESS", "true", "web", "COST", "$5", "gone", "X", "1")

	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "restart", "web"); code != 0 {
		t.Fatalf("restart: %s", stderr)
	}
	want := "-f " + filepath.Join(dir, "compose.json") + " -f " + filepath.Join(dir, "compose.env-overrides.json") + " restart web"
	if !strings.Contains(fakeLog(log), want) {
		t.Fatalf("argv order: want %q in\n%s", want, fakeLog(log))
	}
	b, err := os.ReadFile(filepath.Join(dir, "compose.env-overrides.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 1 || got.Services["web"].Environment["ENABLE_GUEST_ACCESS"] != "true" {
		t.Errorf("derived file: %s", b)
	}
	if got.Services["web"].Environment["COST"] != "$$5" {
		t.Errorf("a $ must be escaped for Compose: %s", b)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "compose.env-overrides.json")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	snap2, _ := os.ReadFile(filepath.Join(dir, "compose.json"))
	m1, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if string(snap) != string(snap2) || m0.ConfigSHA256 != m1.ConfigSHA256 {
		t.Error("the snapshot or its hash changed")
	}

	// no overrides: the file goes and the command line is what it was before
	o, _ := state.ReadEnvOverrides(dir)
	o.Services = map[string]map[string]string{}
	o.Write(dir)
	os.Truncate(log, 0)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "restart", "web"); code != 0 {
		t.Fatalf("restart: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.env-overrides.json")); !os.IsNotExist(err) {
		t.Errorf("derived file should be gone: %v", err)
	}
	if strings.Contains(fakeLog(log), "compose.env-overrides.json") {
		t.Errorf("second -f without overrides:\n%s", fakeLog(log))
	}
}

func TestApplyActionRecreatesOnlyTheService(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	writeOverrides(t, dir, "web", "ENABLE_GUEST_ACCESS", "true")
	os.Truncate(log, 0)
	var out strings.Builder
	code, err := lifecycle.Execute(&out, &out, "apply", root, envBranch, project, dir, stateDir, "", lifecycle.Options{Service: "web"})
	if err != nil || code != 0 {
		t.Fatalf("apply: %d %v %s", code, err, out.String())
	}
	var ups []string
	for _, l := range strings.Split(fakeLog(log), "\n") {
		if strings.HasPrefix(l, "compose ") && strings.Contains(l, " up ") {
			ups = append(ups, l)
		}
	}
	if len(ups) != 1 || !strings.Contains(ups[0], "compose.env-overrides.json") ||
		!strings.Contains(ups[0], " up -d --no-deps --wait --wait-timeout 180 web") || strings.Contains(ups[0], " db") {
		t.Fatalf("up calls: %q", ups)
	}
	if _, err := lifecycle.Execute(&out, &out, "apply", root, envBranch, project, dir, stateDir, "", lifecycle.Options{Service: "nope"}); err == nil {
		t.Error("an unknown service must be refused")
	}
	// a failing apply reports the code and leaves the file
	t.Setenv("DOCKER_FAKE_UP_MSG", "boom")
	t.Setenv("DOCKER_FAKE_UP_ALWAYS", "1")
	code, _ = lifecycle.Execute(&out, &out, "apply", root, envBranch, project, dir, stateDir, "", lifecycle.Options{Service: "web"})
	if code == 0 {
		t.Error("expected a non-zero exit")
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 1 {
		t.Error("a failed apply must leave the file")
	}
}
