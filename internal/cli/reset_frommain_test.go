package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const (
	oldOverride = "services:\n  app:\n    image: alpine:3.20\n    environment:\n      MODE: old\n"
	oldEnv      = "OLD_VALUE=1\n"
	fromMainTok = "SF_TEST_FROMMAIN_TOKEN"
)

// fromMainFixture: main checkout acme-web with a tracked compose.yaml and the
// untracked .stackfork/dev.compose.yaml and runtime.env, prepared with all
// three; linked worktree feat-a with its own (old) untracked override, an old
// runtime.env, prepared with the override alone and a secret reference.
type fromMainEnv struct {
	repo, wt string
	base     []string
	wtDir    string // the worktree instance's state directory
}

func newFromMainEnv(t *testing.T) fromMainEnv {
	t.Helper()
	root, st := reposEnv(t)
	t.Setenv(fromMainTok, "never-written-anywhere")
	repo := scratchRepo(t, filepath.Join(root, "acme-web"))
	os.WriteFile(filepath.Join(repo, "compose.yaml"), []byte(gapBase), 0o644)
	for _, args := range [][]string{{"add", "compose.yaml"}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "compose"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(repo, ".stackfork", "dev.compose.yaml"), []byte(gapOverride), 0o644)
	os.WriteFile(filepath.Join(repo, ".stackfork", "runtime.env"), []byte("DEV_CHECKOUT="+repo+"\n"), 0o600)
	if err := (&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	base := []string{"--state-dir", st, "--legacy-state-dir", t.TempDir()}
	run := func(args ...string) {
		t.Helper()
		if out, e, code := runCLI(t, append(append([]string{}, base...), args...)...); code != 0 {
			t.Fatalf("%v: %s %s", args, out, e)
		}
	}
	run("--root", repo, "prepare", "-f", "compose.yaml", "-f", ".stackfork/dev.compose.yaml", "--env-file", ".stackfork/runtime.env")
	wt := filepath.Join(repo, ".worktrees", "feat-a")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-a", wt).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	os.MkdirAll(filepath.Join(wt, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(wt, ".stackfork", "dev.compose.yaml"), []byte(oldOverride), 0o644)
	os.WriteFile(filepath.Join(wt, ".stackfork", "runtime.env"), []byte(oldEnv), 0o600)
	run("--root", wt, "--secret", "tok=env:"+fromMainTok, "prepare", "-f", ".stackfork/dev.compose.yaml")
	_, dir, err := checkoutInstance(wt, "feat-a", base[1], base[3])
	if err != nil {
		t.Fatal(err)
	}
	return fromMainEnv{repo: repo, wt: wt, base: base, wtDir: dir}
}

func (e fromMainEnv) reset(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runCLI(t, append(append(append([]string{}, e.base...), "--root", e.wt, "reset"), args...)...)
}

func (e fromMainEnv) untouched(t *testing.T, log string, before string) {
	t.Helper()
	if dockerLog(log) != before {
		t.Fatalf("a refusal called Docker:\n%s", strings.TrimPrefix(dockerLog(log), before))
	}
	if _, err := os.Stat(filepath.Join(e.wt, ".stackfork-aside")); err == nil {
		t.Fatal("a refusal moved files aside")
	}
	if b, _ := os.ReadFile(filepath.Join(e.wt, ".stackfork", "dev.compose.yaml")); string(b) != oldOverride {
		t.Fatal("the worktree's override changed")
	}
}

func TestResetFromMainRefusals(t *testing.T) {
	e := newFromMainEnv(t)
	log := installFakeDocker(t)
	before := dockerLog(log)

	// the main checkout
	_, stderr, code := runCLI(t, append(append([]string{}, e.base...), "--root", e.repo, "reset", "--from-main", "--yes")...)
	if code == 0 || !strings.Contains(stderr, "this is the main checkout; reset without --from-main") {
		t.Fatalf("%d %s", code, stderr)
	}
	e.untouched(t, log, before)

	// a main checkout with no prepared instance
	mainDir := func() string { _, d, _ := checkoutInstance(e.repo, "main", e.base[1], e.base[3]); return d }()
	hidden := filepath.Join(t.TempDir(), "main-instance")
	if err := os.Rename(mainDir, hidden); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = e.reset(t, "--from-main", "--yes")
	if code == 0 || !strings.Contains(stderr, "the main checkout has no prepared instance; start it first") {
		t.Fatalf("%d %s", code, stderr)
	}
	e.untouched(t, log, before)
	if err := os.Rename(hidden, mainDir); err != nil {
		t.Fatal(err)
	}

	// an unusable recipe: a tracked file of the main recipe that this worktree lacks
	os.WriteFile(filepath.Join(e.repo, "extra.yaml"), []byte(gapOverride), 0o644)
	for _, args := range [][]string{{"add", "extra.yaml"}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "extra"}} {
		if out, err := exec.Command("git", append([]string{"-C", e.repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	if _, se, code := runCLI(t, append(append([]string{}, e.base...), "--root", e.repo, "prepare", "-f", "compose.yaml", "-f", "extra.yaml", "-f", ".stackfork/dev.compose.yaml")...); code != 0 {
		t.Fatal(se)
	}
	before = dockerLog(log)
	_, stderr, code = e.reset(t, "--from-main", "--yes")
	if code == 0 || !strings.Contains(stderr, "extra.yaml") || !strings.Contains(stderr, "tracked") {
		t.Fatalf("%d %s", code, stderr)
	}
	e.untouched(t, log, before)
}

func TestResetFromMainConfirmationText(t *testing.T) {
	e := newFromMainEnv(t)
	log := installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUMES", "sf-feat-a-data\n")
	before := dockerLog(log)
	out, _, code := askReset(t, "n\n", append(append([]string{}, e.base...), "--root", e.wt, "reset", "--from-main")...)
	if code == 0 {
		t.Fatalf("answering no must not succeed: %s", out)
	}
	pv, _ := PreviewReset(e.wt, e.base[1], e.base[3], true)
	want := "Reset the environment of " + pv.Project + " (feat-a)?\n" +
		"These Docker volumes and their data will be deleted:\n  sf-feat-a-data\n" +
		"Uses the main checkout's recipe: -f compose.yaml -f .stackfork/dev.compose.yaml --env-file .stackfork/runtime.env\n" +
		"Moves these untracked files aside to .stackfork-aside/<UTC time>/ (nothing is deleted):\n" +
		"  .stackfork/dev.compose.yaml\n  .stackfork/runtime.env\n" +
		"Your code is not touched."
	if !strings.HasPrefix(out, want+"\n") {
		t.Fatalf("confirmation:\n%s\nwant:\n%s", out, want)
	}
	if strings.Contains(dockerLog(log), " down ") && strings.Contains(strings.TrimPrefix(dockerLog(log), before), " down ") {
		t.Fatal("answering no ran down")
	}
	if _, err := os.Stat(filepath.Join(e.wt, ".stackfork-aside")); err == nil {
		t.Fatal("answering no moved files")
	}
}

func TestResetFromMainRebuildsFromTheMainRecipe(t *testing.T) {
	e := newFromMainEnv(t)
	log := installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	before := dockerLog(log)
	out, stderr, code := e.reset(t, "--from-main", "--yes")
	if code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, stderr)
	}
	m, err := state.ReadManifest(filepath.Join(e.wtDir, "manifest.json"))
	if err != nil || m == nil {
		t.Fatal(err)
	}
	wantFiles := []string{filepath.Join(e.wt, "compose.yaml"), filepath.Join(e.wt, ".stackfork", "dev.compose.yaml")}
	if strings.Join(m.Files, ",") != strings.Join(wantFiles, ",") || m.EnvFile == nil || *m.EnvFile != filepath.Join(e.wt, ".stackfork", "runtime.env") {
		t.Fatalf("manifest recipe %v %v", m.Files, m.EnvFile)
	}
	if m.ResolvedSecrets()["tok"] != "env:"+fromMainTok {
		t.Fatalf("the instance's own secret references were lost: %v", m.ResolvedSecrets())
	}
	l := strings.TrimPrefix(dockerLog(log), before)
	for _, f := range wantFiles {
		if !strings.Contains(l, f) {
			t.Errorf("prepare did not receive %s:\n%s", f, l)
		}
	}
	d, u := strings.Index(l, " down "), strings.Index(l, " up -d ")
	if d < 0 || u < d {
		t.Fatalf("docker order:\n%s", l)
	}
	if env, _ := os.ReadFile(filepath.Join(e.wt, ".stackfork", "runtime.env")); !strings.Contains(string(env), "DEV_CHECKOUT="+e.wt) {
		t.Fatalf("runtime env not rewritten:\n%s", env)
	}
	if b, _ := os.ReadFile(filepath.Join(e.wt, ".stackfork", "dev.compose.yaml")); string(b) != gapOverride {
		t.Fatalf("override is not the main checkout's:\n%s", b)
	}
	asides, _ := filepath.Glob(filepath.Join(e.wt, ".stackfork-aside", "*", ".stackfork", "dev.compose.yaml"))
	if len(asides) != 1 {
		t.Fatalf("aside files: %v", asides)
	}
	if b, _ := os.ReadFile(asides[0]); string(b) != oldOverride {
		t.Fatal("the old override was not kept")
	}
	if old, _ := filepath.Glob(filepath.Join(e.wt, ".stackfork-aside", "*", ".stackfork", "runtime.env")); len(old) != 1 {
		t.Fatalf("old runtime.env not aside: %v", old)
	}
	for _, want := range []string{"moved .stackfork/dev.compose.yaml aside to .stackfork-aside/", "nothing was deleted; delete .stackfork-aside/", "copied .stackfork/dev.compose.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Missing:") || strings.Contains(stderr, "Missing:") {
		t.Fatalf("a --from-main reset reported a gap:\n%s", out)
	}
	r, err := runs.Get(e.wtDir, 1)
	if err != nil || r.Command != "reset" || r.Outcome != runs.Succeeded {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestResetFromMainDownFailureMovesNothing(t *testing.T) {
	e := newFromMainEnv(t)
	log := installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_EXIT", "1")
	before := dockerLog(log)
	_, _, code := e.reset(t, "--from-main", "--yes")
	if code == 0 {
		t.Fatal("a failing down must fail the reset")
	}
	_ = before
	e.untouched(t, log, dockerLog(log)) // files in place, nothing aside
	if b, _ := os.ReadFile(filepath.Join(e.wt, ".stackfork", "runtime.env")); string(b) != oldEnv {
		t.Fatal("runtime.env changed")
	}
	r, err := runs.Get(e.wtDir, 1)
	if err != nil || r.Outcome != runs.Failed || r.Retry != "stackfork --root "+e.wt+" reset --yes --from-main" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestResetFromMainKeepsTrackedFiles(t *testing.T) {
	e := newFromMainEnv(t)
	installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	// the worktree's compose.yaml is tracked; a local edit must survive
	os.WriteFile(filepath.Join(e.wt, "compose.yaml"), []byte(gapBase+"# local\n"), 0o644)
	if out, se, code := e.reset(t, "--from-main", "--yes"); code != 0 {
		t.Fatalf("%s %s", out, se)
	}
	if b, _ := os.ReadFile(filepath.Join(e.wt, "compose.yaml")); string(b) != gapBase+"# local\n" {
		t.Fatal("a tracked file changed")
	}
}
