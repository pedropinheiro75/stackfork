package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	sfstate "github.com/pedropinheiro75/stackfork/internal/state"
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
	if err := verifiedFixture(&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
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
	if countLines(string(b), " up -d ") != 1 || countLines(string(b), " config ") != 2 { // the secrets step reads the source, then prepare renders it
		t.Fatalf("first start: %s", b)
	}

	out, _, code = runCLI(t, append(base, "start-env", repo)...)
	b, _ = os.ReadFile(log)
	if code != 0 || !strings.Contains(out, "already prepared") || countLines(string(b), " config ") != 2 || countLines(string(b), " up -d ") != 2 { // the secrets step's read is cached per process
		t.Fatalf("second start must not re-prepare: %d %s\n%s", code, out, b)
	}

	// an input change forces prepare again
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(readyCompose+"  cache:\n    image: redis:7\n"), 0o644)
	out, _, code = runCLI(t, append(base, "start-env", repo)...)
	b, _ = os.ReadFile(log)
	if code != 0 || !strings.Contains(out, "preparing") || countLines(string(b), " config ") != 4 { // secrets step + prepare
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
	if err := verifiedFixture(k).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
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
	repo := readyRepo(t, root) // current-schema file, empty plan: starts as before, no trust asked
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
	lifecycle.HostStart = func(out, errOut io.Writer, _ []string, root, instanceDir string, s knowledge.Step) error {
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
	if code != 1 || !strings.Contains(e, "not trusted on this machine") || !strings.Contains(e, "stackfork plan --trust") ||
		!strings.Contains(e, "+ app [host]") || strings.Contains(e, `{"id"`) {
		t.Fatalf("%d %s", code, e)
	}
	if b, _ := os.ReadFile(log); len(strings.TrimSpace(string(b))) != 0 {
		t.Fatalf("docker was called:\n%s", b)
	}
}

// Trust is needed only when the active mode runs a host step.
func TestStartEnvContainerModeNeedsNoTrustForHybridOnlyHostStep(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, append([]knowledge.Step{{ID: "cup", Kind: knowledge.KindComposeUp, Modes: []string{"container"}}}, hybridSteps...))
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	b, _ := os.ReadFile(log)
	if code != 0 || strings.Contains(e, "not trusted") || !strings.Contains(string(b), " up -d ") {
		t.Fatalf("%d %s %s\n%s", code, out, e, b)
	}
}

// A knowledge.json that cannot be loaded is an error, never an empty plan.
func TestStartEnvRefusesBrokenKnowledge(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hybridSteps)
	if err := os.WriteFile(filepath.Join(repo, ".stackfork", "knowledge.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	_, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code == 0 || !strings.Contains(e, "knowledge.json") {
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
	lifecycle.HostStart = func(io.Writer, io.Writer, []string, string, string, knowledge.Step) error { return nil }
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

// Two start-envs at once (the dashboard can do this) must each start their own
// project's host step: nothing about one start may live in a package variable.
func TestConcurrentStartEnvKeepsHostStepsApart(t *testing.T) {
	root, state := reposEnv(t)
	repoA := planRepo(t, root, hybridSteps)
	repoB := planRepo(t, t.TempDir(), hybridSteps)
	installFakeDocker(t)
	var mu sync.Mutex
	got := map[string]string{} // root passed in base -> root passed directly
	old := lifecycle.HostStart
	lifecycle.HostStart = func(_, _ io.Writer, base []string, r, _ string, _ knowledge.Step) error {
		time.Sleep(100 * time.Millisecond) // overlap the two runs
		mu.Lock()
		defer mu.Unlock()
		for i, a := range base {
			if a == "--root" {
				got[base[i+1]] = r
			}
		}
		return nil
	}
	defer func() { lifecycle.HostStart = old }()
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	runCLI(t, "config", "set-mode", repoA, "hybrid")
	runCLI(t, "config", "set-mode", repoB, "hybrid")
	for _, r := range []string{repoA, repoB} {
		if _, e, code := runCLI(t, append(base, "--root", r, "plan", "--trust")...); code != 0 {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	for _, r := range []string{repoA, repoB} {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			var o, e bytes.Buffer
			if err := StartEnv(&o, &e, StartOptions{Checkout: r, NewStateBase: state, LegacyStateBase: t.TempDir(), Mode: "hybrid"}); err != nil {
				t.Errorf("%s: %v %s", r, err, e.String())
			}
		}(r)
	}
	wg.Wait()
	if len(got) != 2 {
		t.Fatalf("each start must reach its own host step: %v", got)
	}
	for baseRoot, r := range got {
		if baseRoot != r {
			t.Errorf("host step got base root %s but root %s", baseRoot, r)
		}
	}
}

const portCompose = "services:\n  db:\n    image: postgres:17\n    volumes:\n      - pgdata:/var/lib/postgresql/data\n  web:\n    image: node:22\n    ports:\n      - \"3000:3000\"\nvolumes:\n  pgdata: {}\n"

// A real schema 1 file with a migrate recipe converts to an exec step with no
// service; every start must still run it, in the primary service.
func TestStartEnvRunsSchema1MigrateInPrimaryService(t *testing.T) {
	root, state := reposEnv(t)
	repo := adaptedRepo(t, root)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(portCompose), 0o644)
	writeKnowledgeJSON(t, repo, `{"schema":1,"status":"verified","recipe":{"migrate":"npm run db:migrate","seed_once":"none"}}`)
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, stderr, code := runCLI(t, append(base, "--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml")...); code != 0 {
		t.Fatal(stderr)
	}
	for i := 1; i <= 2; i++ {
		out, e, code := runCLI(t, append(base, "start-env", repo)...)
		if code != 0 {
			t.Fatalf("start %d: %d %s %s", i, code, out, e)
		}
		var n int
		for _, l := range strings.Split(string(mustRead(t, log)), "\n") {
			if strings.Contains(l, " exec ") && strings.Contains(l, "npm run db:migrate") && strings.Contains(l, " web ") {
				n++
			}
		}
		if n != i {
			t.Fatalf("start %d: migrate ran %d times in web\n%s", i, n, mustRead(t, log))
		}
	}
}

// plan --trust from a worktree reads the plan start-env reads: the worktree's
// own file, else the main repo's.
func TestPlanTrustFromWorktreeUsesMainPlan(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, hybridSteps)
	wt := filepath.Join(repo, ".worktrees", "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "wt", wt).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	if _, err := os.Stat(filepath.Join(wt, ".stackfork", "knowledge.json")); err == nil {
		t.Skip("worktree carries its own knowledge.json")
	}
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	out, e, code := runCLI(t, append(base, "--root", wt, "plan", "--trust")...)
	if code != 0 || !strings.Contains(out, "trusted on this machine") {
		t.Fatalf("%d %s %s", code, out, e)
	}
}

// A machine-wide hybrid mode falls back to container (with a note) for a plan
// without hybrid; a per-repo setting or --mode hybrid still refuses.
func TestGlobalHybridFallsBackToContainer(t *testing.T) {
	root, state := reposEnv(t)
	repo := planRepo(t, root, []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}})
	log := installFakeDocker(t)
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, "config", "set", "mode", "hybrid"); code != 0 {
		t.Fatal(e)
	}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if code != 0 || strings.Count(out, "using container") != 1 || !strings.Contains(string(mustRead(t, log)), " up -d ") {
		t.Fatalf("global hybrid must fall back: %d %s %s", code, out, e)
	}
	if _, e, code := runCLI(t, append(base, "start-env", "--mode", "hybrid", repo)...); code == 0 || !strings.Contains(e, `mode "hybrid" is not supported`) {
		t.Fatalf("--mode hybrid must refuse: %d %s", code, e)
	}
	runCLI(t, "config", "set-mode", repo, "hybrid")
	if _, e, code := runCLI(t, append(base, "start-env", repo)...); code == 0 || !strings.Contains(e, `mode "hybrid" is not supported`) {
		t.Fatalf("a per-repo hybrid must refuse: %d %s", code, e)
	}
}

// In hybrid the container-only data step must not run (its service is the
// excluded app) and must not be burned: a later container start seeds, in the
// plan's app service.
func TestHybridStartLeavesContainerSeedPending(t *testing.T) {
	root, state := reposEnv(t)
	repo := adaptedRepo(t, root)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(planCompose), 0o644)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, AppService: "db", Steps: []knowledge.Step{
			{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{"container"}},
			{ID: "up-h", Kind: knowledge.KindComposeUp, Modes: []string{"hybrid"}, Exclude: []string{"web"}},
			{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Modes: []string{"container"}, Cmd: "s-cmd"},
			{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "npm run dev"},
		}}}
	if err := verifiedFixture(k).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	log := installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1") // a new volume: an up that seeds would seed
	old := lifecycle.HostStart
	lifecycle.HostStart = func(io.Writer, io.Writer, []string, string, string, knowledge.Step) error { return nil }
	defer func() { lifecycle.HostStart = old }()
	base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "--root", repo, "plan", "--trust")...); code != 0 {
		t.Fatal(e)
	}
	if out, e, code := runCLI(t, append(base, "start-env", "--mode", "hybrid", repo)...); code != 0 {
		t.Fatalf("hybrid: %d %s %s", code, out, e)
	}
	if b := string(mustRead(t, log)); strings.Contains(b, " exec ") {
		t.Fatalf("hybrid ran the container-only seed:\n%s", b)
	}
	ms, _ := filepath.Glob(filepath.Join(state, "*", "manifest.json"))
	if len(ms) != 1 {
		t.Fatalf("manifests: %v", ms)
	}
	if m, _ := sfstate.ReadManifest(ms[0]); m == nil || m.Seeded != nil || m.SeedPending {
		t.Fatalf("seed must stay undecided: %+v", m)
	}
	if out, e, code := runCLI(t, append(base, "start-env", "--mode", "container", repo)...); code != 0 {
		t.Fatalf("container: %d %s %s", code, out, e)
	}
	n := 0
	for _, l := range strings.Split(string(mustRead(t, log)), "\n") {
		if strings.Contains(l, " exec ") && strings.Contains(l, "s-cmd") && strings.Contains(l, " db ") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the container start must seed once in the app service:\n%s", mustRead(t, log))
	}
}

func seedModeRepo(t *testing.T, root string) string {
	t.Helper()
	repo := adaptedRepo(t, root)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(planCompose), 0o644)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, AppService: "db", Steps: []knowledge.Step{
			{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{"container"}},
			{ID: "up-h", Kind: knowledge.KindComposeUp, Modes: []string{"hybrid"}, Exclude: []string{"web"}},
			{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Modes: []string{"container"}, Cmd: "s-cmd"},
			{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "npm run dev"},
		}}}
	if err := verifiedFixture(k).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	return repo
}

// The mode given to start-env decides the seed end to end, whatever the
// configured mode says: no empty command runs, nothing is burned.
func TestSeedUsesTheRequestedModeNotTheConfiguredOne(t *testing.T) {
	for _, c := range []struct{ configured, requested string }{{"hybrid", "container"}, {"container", "hybrid"}} {
		t.Run(c.requested, func(t *testing.T) {
			root, state := reposEnv(t)
			repo := seedModeRepo(t, root)
			log := installFakeDocker(t)
			t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
			old := lifecycle.HostStart
			lifecycle.HostStart = func(io.Writer, io.Writer, []string, string, string, knowledge.Step) error { return nil }
			defer func() { lifecycle.HostStart = old }()
			base := []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
			runCLI(t, append(base, "--root", repo, "plan", "--trust")...)
			if _, e, code := runCLI(t, "config", "set-mode", repo, c.configured); code != 0 {
				t.Fatal(e)
			}
			if out, e, code := runCLI(t, append(base, "start-env", "--mode", c.requested, repo)...); code != 0 {
				t.Fatalf("%d %s %s", code, out, e)
			}
			var execs []string
			for _, l := range strings.Split(string(mustRead(t, log)), "\n") {
				if strings.Contains(l, " exec ") {
					execs = append(execs, l)
				}
			}
			ms, _ := filepath.Glob(filepath.Join(state, "*", "manifest.json"))
			m, _ := sfstate.ReadManifest(ms[0])
			if c.requested == "container" {
				if len(execs) != 1 || !strings.Contains(execs[0], "s-cmd") || m.Seeded == nil || !*m.Seeded {
					t.Fatalf("container must seed once with the real command: %v %+v", execs, m.Seeded)
				}
			} else if len(execs) != 0 || m.Seeded != nil {
				t.Fatalf("hybrid must not seed or burn it: %v %+v", execs, m.Seeded)
			}
		})
	}
}
