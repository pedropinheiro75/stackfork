package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestResetKeepsEnvOverridesAndSaysSo(t *testing.T) {
	repo, stateBase, base, _ := resetFixture(t)
	dir := onlyRunDir(t, stateBase)
	writeOverrides(t, dir, "web", "ENABLE_GUEST_ACCESS", "true")
	out, e, code := runCLI(t, append(base, "--root", repo, "reset", "--yes")...)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if !strings.Contains(out, "keeping 1 env overrides (stackfork env overrides --clear to drop)") {
		t.Errorf("missing the note:\n%s", out)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 1 {
		t.Errorf("overrides after reset: %+v", o)
	}
}

func TestResetClearEnvDropsOverrides(t *testing.T) {
	repo, stateBase, base, _ := resetFixture(t)
	dir := onlyRunDir(t, stateBase)
	writeOverrides(t, dir, "web", "A", "1", "db", "B", "2")
	out, e, code := runCLI(t, append(base, "--root", repo, "reset", "--yes", "--clear-env")...)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if _, err := os.Stat(filepath.Join(dir, state.EnvOverridesFile)); !os.IsNotExist(err) {
		t.Errorf("file should be gone: %v", err)
	}
	if !strings.Contains(out, "dropped 2 env overrides") {
		t.Errorf("output:\n%s", out)
	}
}

func TestDownForgetRemovesEnvOverrideFiles(t *testing.T) {
	_, root, stateDir, _, dir := envFixture(t)
	writeOverrides(t, dir, "web", "A", "1")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "restart", "web"); code != 0 { // writes the derived file
		t.Fatal(stderr)
	}
	for _, f := range []string{state.EnvOverridesFile, "compose.env-overrides.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down"); code != 0 {
		t.Fatal(stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, state.EnvOverridesFile)); err != nil {
		t.Fatalf("down must keep the overrides: %v", err)
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--volumes", "--forget"); code != 0 {
		t.Fatal(stderr)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("instance dir must be gone: %v", err)
	}
}
