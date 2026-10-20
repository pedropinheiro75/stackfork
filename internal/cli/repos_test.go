package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// scratchRepo makes a one-commit repo on main at dir.
func scratchRepo(t *testing.T, dir string) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	for _, args := range [][]string{{"add", "f"}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	r, _ := filepath.EvalSymlinks(dir)
	return r
}

func reposEnv(t *testing.T) (root, stateDir string) {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	installFakeDocker(t)
	root = t.TempDir()
	stateDir = t.TempDir()
	return
}

func TestReposNoRootsExplainsHowToAddOne(t *testing.T) {
	_, state := reposEnv(t)
	out, _, code := runCLI(t, "--state-dir", state, "--legacy-state-dir", t.TempDir(), "repos")
	if code != 0 || !strings.Contains(out, "stackfork config add-root PATH") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestReposListsJSONTableAndPrompt(t *testing.T) {
	root, state := reposEnv(t)
	repo := scratchRepo(t, filepath.Join(root, "Group", "proj"))
	if _, e, code := runCLI(t, "config", "add-root", root); code != 0 {
		t.Fatal(e)
	}
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "repos", "--json")...)
	if code != 0 {
		t.Fatal(e)
	}
	var res struct {
		Repos []struct{ Name, Path, Group, Readiness string }
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Repos) != 1 {
		t.Fatalf("%v %s", err, out)
	}
	if got := res.Repos[0]; got.Name != "proj" || got.Path != repo || got.Group != "Group" || got.Readiness != "unknown" {
		t.Fatalf("%+v", got)
	}
	table, _, _ := runCLI(t, append(base, "repos")...)
	if !strings.Contains(table, "proj") || !strings.Contains(table, "unknown") {
		t.Fatalf("%s", table)
	}
	p, _, code := runCLI(t, append(base, "repos", "--prompt", "proj", "feat/x")...)
	if code != 0 || !strings.Contains(p, "`proj`") || !strings.Contains(p, "feat/x") || !strings.Contains(p, "stackfork worktree add") {
		t.Fatalf("%d %s", code, p)
	}
	if _, e, code := runCLI(t, append(base, "repos", "--prompt", "nope", "x")...); code == 0 || !strings.Contains(e, "no repo") {
		t.Fatalf("%d %s", code, e)
	}
	if _, _, code := runCLI(t, append(base, "repos", "--rescan")...); code != 0 {
		t.Fatal("rescan failed")
	}
}

func TestWorktreeAddByNameAndPath(t *testing.T) {
	root, state := reposEnv(t)
	repo := scratchRepo(t, filepath.Join(root, "proj"))
	runCLI(t, "config", "add-root", root)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}

	out, e, code := runCLI(t, append(base, "worktree", "add", "proj", "feat/x", "--from", "main")...)
	if code != 0 || !strings.Contains(out, filepath.Join(repo, ".worktrees", "feat-x")) {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "feat-x", "f")); err != nil {
		t.Fatal(err)
	}
	if _, e, code := runCLI(t, append(base, "worktree", "add", repo, "feat/x")...); code == 0 || !strings.Contains(e, "already exists") {
		t.Fatalf("%d %s", code, e)
	}
	if _, e, code := runCLI(t, append(base, "worktree", "add", "nope", "x")...); code == 0 || !strings.Contains(e, "no repo") {
		t.Fatalf("%d %s", code, e)
	}
	if _, e, code := runCLI(t, append(base, "worktree", "add", "proj")...); code == 0 || !strings.Contains(e, "usage") {
		t.Fatalf("%d %s", code, e)
	}
}

const readyCompose = "services:\n  db:\n    image: postgres:17\n"

// adaptedRepo is a scratch repo with an adapter compose file: it could run,
// but nothing verified how, so it needs learn.
func adaptedRepo(t *testing.T, root string) string {
	t.Helper()
	repo := scratchRepo(t, filepath.Join(root, "ready"))
	os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(readyCompose), 0o644)
	return repo
}

// readyRepo is an adapted repo whose startup was learned and verified.
func readyRepo(t *testing.T, root string) string {
	t.Helper()
	repo := adaptedRepo(t, root)
	if err := (&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return repo
}

func countLines(log, sub string) int { return strings.Count(log, sub) }

func TestStartEnvPreparesOnlyWhenNeededThenUps(t *testing.T) {
	root, state := reposEnv(t)
	repo := readyRepo(t, root)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}

	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if !strings.Contains(out, "preparing") || !strings.Contains(out, "started") {
		t.Fatalf("progress lines missing: %s", out)
	}
	b, _ := os.ReadFile(log)
	if countLines(string(b), " up -d ") != 1 || countLines(string(b), " config ") != 1 {
		t.Fatalf("first start: %s", b)
	}

	out, _, code = runCLI(t, append(base, "start-env", repo)...)
	b, _ = os.ReadFile(log)
	if code != 0 || !strings.Contains(out, "already prepared") || countLines(string(b), " config ") != 1 || countLines(string(b), " up -d ") != 2 {
		t.Fatalf("second start must not re-prepare: %d %s\n%s", code, out, b)
	}

	// an input change forces prepare again
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(readyCompose+"  cache:\n    image: redis:7\n"), 0o644)
	out, _, code = runCLI(t, append(base, "start-env", repo)...)
	b, _ = os.ReadFile(log)
	if code != 0 || !strings.Contains(out, "preparing") || countLines(string(b), " config ") != 2 {
		t.Fatalf("changed input must re-prepare: %d %s\n%s", code, out, b)
	}
}

func TestStartEnvRefusesUnknownWithoutStartingAnything(t *testing.T) {
	root, state := reposEnv(t)
	repo := scratchRepo(t, filepath.Join(root, "mystery"))
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "unknown") || !strings.Contains(e, "repos --prompt") {
		t.Fatalf("%d %s", code, e)
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), " up ") || strings.Contains(string(b), " config ") {
		t.Fatalf("docker was used for an unknown repo:\n%s", b)
	}
}

func TestStartEnvRefusesNeedsLearnUnlessForced(t *testing.T) {
	root, state := reposEnv(t)
	repo := adaptedRepo(t, root)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "needs-learn") || !strings.Contains(e, "repos --prompt") {
		t.Fatalf("%d %s", code, e)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), " up ") {
		t.Fatalf("started a needs-learn repo:\n%s", b)
	}
	out, e, code := runCLI(t, append(base, "start-env", "--force", repo)...)
	if code != 0 || !strings.Contains(e, "warning") || !strings.Contains(out, "started") {
		t.Fatalf("--force: %d %s %s", code, out, e)
	}
}

func TestRecipePrepareArgs(t *testing.T) {
	got := recipePrepareArgs(`stackfork --root "$CHECKOUT" prepare -f .agent-dev/a.compose.yaml --env-file '.agent-dev/runtime.env' --random-ports`)
	want := []string{"-f", ".agent-dev/a.compose.yaml", "--env-file", ".agent-dev/runtime.env", "--random-ports"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", got)
	}
	for _, bad := range []string{"stackfork prepare -f $X/a.yaml", "stackfork prepare --npmrc /x", "stackfork up", "stackfork prepare -f a.yaml && rm -rf x"} {
		if recipePrepareArgs(bad) != nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestStartEnvInWorktreeMissingAnUntrackedAdapterSaysSo(t *testing.T) {
	root, state := reposEnv(t)
	repo := readyRepo(t, root) // the adapter is untracked: a new worktree will not have it
	installFakeDocker(t)
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "wt", filepath.Join(repo, ".worktrees", "wt")).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", filepath.Join(repo, ".worktrees", "wt"))...)
	if code == 0 || !strings.Contains(e, "only in the main checkout") || !strings.Contains(e, "adapter_files") {
		t.Fatalf("%d %s", code, e)
	}
}

const planCompose = "services:\n  db:\n    image: postgres:17\n    volumes:\n      - pgdata:/var/lib/postgresql/data\n  web:\n    image: nginx\nvolumes:\n  pgdata: {}\n"

// planRepo is a verified repo whose knowledge carries the given plan steps.
func planRepo(t *testing.T, root string, steps []knowledge.Step) string {
	t.Helper()
	repo := adaptedRepo(t, root)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(planCompose), 0o644)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: steps}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return repo
}

var hybridSteps = []knowledge.Step{
	{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{"hybrid"}, Exclude: []string{"web"}},
	{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "m"},
	{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "npm run dev"},
}

func TestStartEnvRunsPlanInContainerMode(t *testing.T) {
	root, state := reposEnv(t)
	repo := readyRepo(t, root) // schema 1 conversion, empty recipe: starts as before, no trust asked
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	b, _ := os.ReadFile(log)
	if code != 0 || !strings.Contains(out, "started") || countLines(string(b), " up -d ") != 1 || strings.Contains(string(b), " exec ") {
		t.Fatalf("%d %s %s\n%s", code, out, e, b)
	}
}

func TestStartEnvHybridExcludesAppAndRunsHostStep(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hybridSteps)
	log := installFakeDocker(t)
	var started []string
	old := lifecycle.HostStart
	lifecycle.HostStart = func(out, errOut io.Writer, root, instanceDir string, s knowledge.Step) error {
		started = append(started, s.Name)
		return nil
	}
	defer func() { lifecycle.HostStart = old }()
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "--root", repo, "plan", "--trust")...); code != 0 {
		t.Fatal(e)
	}
	if _, e, code := runCLI(t, "config", "set-mode", repo, "hybrid"); code != 0 {
		t.Fatal(e)
	}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	b := string(mustRead(t, log))
	var upLine, execLine string
	for _, l := range strings.Split(b, "\n") {
		if strings.Contains(l, " up -d ") {
			upLine = strings.TrimSpace(l)
		}
		if strings.Contains(l, " exec ") {
			execLine = l
		}
	}
	if code != 0 || !strings.HasSuffix(upLine, " db") || strings.Contains(upLine, "web") || !strings.Contains(execLine, " m") ||
		len(started) != 1 || started[0] != "app" {
		t.Fatalf("%d %s %s\nup=%q exec=%q started=%v", code, out, e, upLine, execLine, started)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStartEnvRefusesUntrustedBeforeDocker(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hybridSteps)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, "config", "set-mode", repo, "hybrid")
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code != 1 || !strings.Contains(e, "not trusted on this machine") || !strings.Contains(e, "stackfork plan --trust") {
		t.Fatalf("%d %s", code, e)
	}
	if b, _ := os.ReadFile(log); len(strings.TrimSpace(string(b))) != 0 {
		t.Fatalf("docker was called:\n%s", b)
	}
}

func TestStartEnvRefusesUnsupportedMode(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}})
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", "--mode", "hybrid", repo)...)
	if code == 0 || !strings.Contains(e, `mode "hybrid" is not supported`) || !strings.Contains(e, "supported: container") {
		t.Fatalf("%d %s", code, e)
	}
	if b, _ := os.ReadFile(log); len(strings.TrimSpace(string(b))) != 0 {
		t.Fatalf("docker was called:\n%s", b)
	}
}

func TestStartEnvSkipsSchemaStepWhenUpSeeded(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "m"},
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "db", Cmd: "s"},
	})
	log := installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1") // the volume is new: this up seeds
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	b := string(mustRead(t, log))
	if code != 0 || countLines(b, " exec ") != 1 || !strings.Contains(b, "m && s") {
		t.Fatalf("%d %s %s\n%s", code, out, e, b)
	}
}

const depCompose = "services:\n  db:\n    image: postgres:17\n  web:\n    image: nginx\n  nginx:\n    image: nginx\n    depends_on:\n      - web\n"

func hybridRepo(t *testing.T, root, compose string, steps []knowledge.Step) (repo, state string, base []string) {
	repo = planRepo(t, root, steps)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(compose), 0o644)
	return repo, "", nil
}

func TestHybridExcludeDropsDependents(t *testing.T) {
	root, state := reposEnv(t)
	repo, _, _ := hybridRepo(t, root, depCompose, hybridSteps)
	log := installFakeDocker(t)
	old := lifecycle.HostStart
	lifecycle.HostStart = func(io.Writer, io.Writer, string, string, knowledge.Step) error { return nil }
	defer func() { lifecycle.HostStart = old }()
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, "config", "set-mode", repo, "hybrid")
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	var upLine string
	for _, l := range strings.Split(string(mustRead(t, log)), "\n") {
		if strings.Contains(l, " up -d ") {
			upLine = strings.TrimSpace(l)
		}
	}
	if code != 0 || !strings.HasSuffix(upLine, " db") || strings.Contains(upLine, "nginx") || !strings.Contains(out, "hybrid: not starting nginx (they depend on web)") {
		t.Fatalf("%d %s %s\n%s", code, out, e, upLine)
	}
}

func TestHybridExcludeAllErrors(t *testing.T) {
	root, state := reposEnv(t)
	repo, _, _ := hybridRepo(t, root, "services:\n  web:\n    image: nginx\n", hybridSteps)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, "config", "set-mode", repo, "hybrid")
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "step up: exclude leaves no services to start") || strings.Contains(string(mustRead(t, log)), " up -d ") {
		t.Fatalf("%d %s", code, e)
	}
}

func TestSchemaStepOnExcludedServiceRefusedBeforeDocker(t *testing.T) {
	root, state := reposEnv(t)
	steps := []knowledge.Step{hybridSteps[0], {ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "web", Cmd: "m"}, hybridSteps[2]}
	repo := planRepo(t, root, steps)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, "config", "set-mode", repo, "hybrid")
	runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if b, _ := os.ReadFile(log); code == 0 || !strings.Contains(e, "step migrate runs in web, which this mode does not start; set --service on the step") || len(strings.TrimSpace(string(b))) != 0 {
		t.Fatalf("%d %s\n%s", code, e, b)
	}
}
