package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// keptRefFixture prepares a planned repo with --npmrc (a fake file, never a
// real one) and returns the reference the manifest must keep.
func keptRefFixture(t *testing.T) (repo, stateDir string, base []string, want string) {
	t.Helper()
	root, stateDir := reposEnv(t)
	repo = planRepo(t, root, resetSteps)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	npmrc := filepath.Join(t.TempDir(), ".npmrc")
	if err := os.WriteFile(npmrc, []byte("//npm.pkg.github.com/:_authToken=synthetic-keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base = []string{"--state-dir", stateDir, "--legacy-state-dir", t.TempDir()}
	if out, e, code := runCLI(t, append(base, "--root", repo, "--npmrc", npmrc, "prepare", "-f", ".agent-dev/app.compose.yaml")...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	resolved, _ := identity.ResolvePath(npmrc)
	want = "npmrc:" + resolved + "#npm.pkg.github.com"
	assertKeptRef(t, stateDir, want, "first prepare")
	return repo, stateDir, base, want
}

func assertKeptRef(t *testing.T, stateDir, want, after string) {
	t.Helper()
	m, err := state.ReadManifest(filepath.Join(onlyRunDir(t, stateDir), "manifest.json"))
	if err != nil || m == nil {
		t.Fatalf("after %s: %v", after, err)
	}
	if got := m.ResolvedSecrets()["npm_token"]; got != want {
		t.Fatalf("after %s: npm_token = %q, want %q", after, got, want)
	}
}

func TestReprepareKeepsSecretRefs(t *testing.T) {
	repo, stateDir, base, want := keptRefFixture(t)
	steps := []struct {
		name string
		args []string
	}{
		{"start-env", []string{"start-env", repo}},
		{"prepare without flags", []string{"--root", repo, "prepare"}},
		{"prepare -f again", []string{"--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml"}},
		{"reset", []string{"--root", repo, "reset", "--yes"}},
		{"start-env after reset", []string{"start-env", repo}},
	}
	for i, s := range steps {
		if i == 1 { // the recipe changed: start-env prepares again
			os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(planCompose+"# changed\n"), 0o644)
			s = struct {
				name string
				args []string
			}{"start-env after a recipe change", []string{"start-env", repo}}
			if out, e, code := runCLI(t, append(base, s.args...)...); code != 0 {
				t.Fatalf("%s: %d %s %s", s.name, code, out, e)
			}
			assertKeptRef(t, stateDir, want, s.name)
			s = steps[i]
		}
		if out, e, code := runCLI(t, append(base, s.args...)...); code != 0 {
			t.Fatalf("%s: %d %s %s", s.name, code, out, e)
		}
		assertKeptRef(t, stateDir, want, s.name)
	}
}

func TestResetFromMainKeepsSecretRefs(t *testing.T) {
	e := newFromMainEnv(t)
	want := "env:" + fromMainTok
	check := func(after string) {
		t.Helper()
		m, _ := state.ReadManifest(filepath.Join(e.wtDir, "manifest.json"))
		if m == nil || m.ResolvedSecrets()["tok"] != want {
			t.Fatalf("after %s: %+v", after, m)
		}
	}
	check("prepare")
	if out, er, code := e.reset(t, "--yes", "--from-main"); code != 0 {
		t.Fatalf("%d %s %s", code, out, er)
	}
	check("reset --from-main")
	if out, er, code := runCLI(t, append(append([]string{}, e.base...), "start-env", e.wt)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, er)
	}
	check("start-env")
}

func TestRemoveThenStartKeepsSecretRefs(t *testing.T) {
	repo, stateDir, base, want := keptRefFixture(t)
	name := filepath.Base(onlyRunDir(t, stateDir))
	if out, e, code := runCLI(t, append(base, "--instance", name, "down", "--volumes", "--forget")...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if out, e, code := runCLI(t, append(base, "list")...); code != 0 || strings.Contains(out, name) {
		t.Fatalf("a removed instance must leave the list: %d %s %s", code, out, e)
	}
	if out, e, code := runCLI(t, append(base, "start-env", repo)...); code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	assertKeptRef(t, stateDir, want, "Remove instance and data, then Start")
	if _, err := os.Stat(filepath.Join(stateDir, name, state.KeptSecrets)); !os.IsNotExist(err) {
		t.Fatalf("the manifest holds the references again; the kept file must go: %v", err)
	}
}

// An orphan (its checkout is gone) keeps nothing: no Start can prepare it again.
func TestRemoveOrphanKeepsNothing(t *testing.T) {
	_, stateDir, _, _ := keptRefFixture(t)
	dir := onlyRunDir(t, stateDir)
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	m.Root = filepath.Join(t.TempDir(), "gone")
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.ForgetInstance(io.Discard, dir, stateDir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("orphan state must go entirely: %v", err)
	}
}
