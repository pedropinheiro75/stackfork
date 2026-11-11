package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func TestLearnVerifyRefusesPreparedButNeverExecutedRecipe(t *testing.T) {
	v := newRecipeVerify(t, fullPrepare, []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: fullPrepare},
		{ID: "up", Kind: knowledge.KindComposeUp},
	})
	_, stderr, code := v.verify(t)
	if code == 0 || !strings.Contains(stderr, "start-env") {
		t.Fatalf("unexecuted recipe verified: code=%d stderr=%s", code, stderr)
	}
}

func TestOneOffStartupAndVerification(t *testing.T) {
	v := newRecipeVerify(t, fullPrepare, []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: fullPrepare},
		{ID: "dependencies", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}},
		{ID: "install", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "app", Cmd: "install-packages", Once: knowledge.OnceDeps},
		{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "app", Cmd: "migrate-schema"},
		{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "app", Cmd: "seed-data", Once: knowledge.OnceData},
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "check", Kind: knowledge.KindExec, Service: "app", Cmd: "check-server"},
	})
	logPath := installFakeDocker(t)
	if err := os.WriteFile(filepath.Join(v.root, "compose.yaml"), []byte(gapBase+"  db:\n    image: postgres:17\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, stderr, code := runCLI(t, append(append(append([]string{}, v.flags...), "--root", v.root, "prepare"), fullPrepare...)...); code != 0 {
		t.Fatalf("prepare: %d %s %s", code, out, stderr)
	}
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	start := func() {
		t.Helper()
		if out, stderr, code := runCLI(t, append(append([]string{}, v.flags...), "start-env", v.root)...); code != 0 {
			t.Fatalf("start: %d %s %s", code, out, stderr)
		}
	}
	start()
	log, _ := os.ReadFile(logPath)
	for _, cmd := range []string{"install-packages", "migrate-schema", "seed-data"} {
		if !strings.Contains(string(log), "run --rm --no-deps -T app sh -c "+cmd) {
			t.Fatalf("%s did not use a one-off container:\n%s", cmd, log)
		}
	}
	if !strings.Contains(string(log), "exec -T app sh -c check-server") {
		t.Fatal("ordinary exec changed to a temporary container")
	}
	if out, stderr, code := v.verify(t); code != 0 {
		t.Fatalf("verify: %d %s %s", code, out, stderr)
	}
	k, err := knowledge.Load(v.path)
	if err != nil || k.Status != knowledge.StatusVerified || !k.VerificationCurrent() {
		t.Fatalf("verification not bound to the executed recipe: %+v %v", k, err)
	}
	_, _, dir, _, err := checkoutManifest(v.root, v.flags[1], v.flags[3])
	if err != nil {
		t.Fatal(err)
	}
	history, _ := runs.List(dir)
	if len(history) == 0 || history[0].RecipeHash != k.Plan.ExecutionHash() {
		t.Fatal("successful run has no recipe fingerprint")
	}
	start()
	log, _ = os.ReadFile(logPath)
	if strings.Count(string(log), "run --rm --no-deps -T app sh -c install-packages") != 1 || strings.Count(string(log), "run --rm --no-deps -T app sh -c seed-data") != 1 {
		t.Fatalf("restart repeated installation or seed:\n%s", log)
	}
	for _, index := range []int{2, 4} {
		original := k.Plan.Steps[index].Cmd
		k.Plan.Steps[index].Cmd = original + "-changed"
		if err := k.Save(v.path); err != nil {
			t.Fatal(err)
		}
		start()
		if _, stderr, code := v.verify(t); code == 0 || !strings.Contains(stderr, "execution evidence") {
			t.Fatalf("changed once task verified without execution: %d %s", code, stderr)
		}
		log, _ := os.ReadFile(logPath)
		if strings.Contains(string(log), "sh -c "+original+"-changed") {
			t.Fatal("a changed once task was forced on existing data")
		}
		k.Plan.Steps[index].Cmd = original
		if err := k.Save(v.path); err != nil {
			t.Fatal(err)
		}
		start()
	}
	k.Plan.Steps[3].Cmd = "changed-migration"
	if err := k.Save(v.path); err != nil {
		t.Fatal(err)
	}
	k, _ = knowledge.Load(v.path)
	if k.Status == knowledge.StatusVerified {
		t.Fatal("edited recipe retained verification")
	}
	if _, stderr, code := v.verify(t); code == 0 || !strings.Contains(stderr, "start-env") {
		t.Fatalf("old run verified a changed recipe: %d %s", code, stderr)
	}
}

func TestVerificationRejectsNonmatchingOrIncompleteRuns(t *testing.T) {
	for _, scenario := range []string{"wrong-mode", "wrong-recipe", "plain-up", "live", "failed", "running", "wrong-snapshot", "missing-snapshot", "latest-failed"} {
		t.Run(scenario, func(t *testing.T) {
			v := newRecipeVerify(t, fullPrepare, []knowledge.Step{
				{ID: "prepare", Kind: knowledge.KindPrepare, Args: fullPrepare},
				{ID: "up", Kind: knowledge.KindComposeUp},
			})
			k, _ := knowledge.Load(v.path)
			_, project, dir, manifest, err := checkoutManifest(v.root, v.flags[1], v.flags[3])
			if err != nil {
				t.Fatal(err)
			}
			meta := runs.Meta{Command: "start-env", Mode: knowledge.ModeContainer, Project: project, RecipeHash: k.Plan.ExecutionHash()}
			switch scenario {
			case "wrong-mode":
				meta.Mode = knowledge.ModeHybrid
			case "wrong-recipe":
				meta.RecipeHash = "different"
			case "plain-up":
				meta.Command = "up"
			case "live":
				meta.Command = "live"
			}
			if scenario == "latest-failed" {
				recordVerifyRun(t, v.root, v.flags)
			}
			rec, err := runs.Begin(dir, meta)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := manifest.ConfigSHA256
			if scenario == "wrong-snapshot" {
				snapshot = "different"
			}
			if scenario != "missing-snapshot" {
				rec.SetSnapshotHash(snapshot)
			}
			switch scenario {
			case "failed", "latest-failed":
				rec.Finish(errors.New("setup failed"))
			case "running":
				defer rec.Finish(errors.New("test finished"))
			default:
				rec.Finish(nil)
			}
			if _, stderr, code := v.verify(t); code == 0 || !strings.Contains(stderr, "start-env") {
				t.Fatalf("invalid evidence accepted: %d %s", code, stderr)
			}
		})
	}
}

func TestOneOffTasksReusePreflightSecrets(t *testing.T) {
	repo, base, _, _ := preflightFixture(t, knowledge.SecretDecl{Name: "NPM_TOKEN"})
	const canary = "one-off-preflight-canary"
	calls := fakeOp(t, canary)
	mapSecret(t, repo, "NPM_TOKEN", "op://test/item/token")
	compose := "services:\n  db:\n    image: postgres:17\n    volumes: [data:/data]\n    secrets: [NPM_TOKEN]\nvolumes:\n  data: {}\nsecrets:\n  NPM_TOKEN:\n    environment: NPM_TOKEN\n"
	if err := os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	k, _ := knowledge.Load(knowledgePath(repo))
	k.Plan = knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", ".agent-dev/app.compose.yaml"}},
		{ID: "dependencies", Kind: knowledge.KindComposeUp},
		{ID: "install", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "db", Cmd: "install", Once: knowledge.OnceDeps},
		{ID: "migrate", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "db", Cmd: "migrate"},
		{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "db", Cmd: "seed", Once: knowledge.OnceData},
	}}
	if err := verifiedFixture(k).Save(knowledgePath(repo)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	t.Setenv("DOCKER_FAKE_EXEC_OUT", canary)
	out, stderr, code := runCLI(t, append(base, "start-env", repo)...)
	if code != 0 || strings.Contains(out+stderr, canary) {
		t.Fatalf("one-off setup failed or exposed a secret: %d %s %s", code, out, stderr)
	}
	lookups, _ := os.ReadFile(calls)
	if strings.Count(string(lookups), "read ") != 1 {
		t.Fatalf("secrets resolved after preflight: %s", lookups)
	}
	_, _, dir, _, err := checkoutManifest(repo, base[1], base[3])
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"seed", "up"} {
		setSeeded(dir, boolPtr(false))
		if command == "up" {
			k.Plan.Steps = []knowledge.Step{
				{ID: "up", Kind: knowledge.KindComposeUp},
				{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "db", Cmd: "seed", Once: knowledge.OnceData},
			}
			if err := k.Save(knowledgePath(repo)); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(calls, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		out, stderr, code := runCLI(t, append(append([]string{}, base...), "--root", repo, command)...)
		if code != 0 || strings.Contains(out+stderr, canary) {
			t.Fatalf("%s failed or exposed a secret: %d %s %s", command, code, out, stderr)
		}
		lookups, _ := os.ReadFile(calls)
		if strings.Count(string(lookups), "read ") != 1 {
			t.Fatalf("%s resolved secrets after preflight: %s", command, lookups)
		}
	}
}

// oneOffVerify prepares a fake-docker instance whose recipe has a one-off
// install without an explicit service, and returns a start-env runner.
func oneOffVerify(t *testing.T, steps []knowledge.Step, appService string) (recipeVerify, func(), string) {
	t.Helper()
	steps = append([]knowledge.Step{{ID: "prepare", Kind: knowledge.KindPrepare, Args: fullPrepare}}, steps...)
	v := newRecipeVerify(t, fullPrepare, steps)
	k, _ := knowledge.Load(v.path)
	k.Plan.AppService = appService
	if err := k.Save(v.path); err != nil {
		t.Fatal(err)
	}
	logPath := installFakeDocker(t)
	if err := os.WriteFile(filepath.Join(v.root, "compose.yaml"), []byte(gapBase+"  db:\n    image: postgres:17\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, stderr, code := runCLI(t, append(append(append([]string{}, v.flags...), "--root", v.root, "prepare"), fullPrepare...)...); code != 0 {
		t.Fatalf("prepare: %d %s %s", code, out, stderr)
	}
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	start := func() {
		t.Helper()
		if out, stderr, code := runCLI(t, append(append([]string{}, v.flags...), "start-env", v.root)...); code != 0 {
			t.Fatalf("start: %d %s %s", code, out, stderr)
		}
	}
	return v, start, logPath
}

func TestExecutionEvidenceFollowsTheResolvedService(t *testing.T) {
	v, start, logPath := oneOffVerify(t, []knowledge.Step{
		{ID: "dependencies", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}},
		{ID: "install", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Cmd: "install-packages", Once: knowledge.OnceDeps},
		{ID: "up", Kind: knowledge.KindComposeUp},
	}, "app")
	start()
	if out, stderr, code := v.verify(t); code != 0 {
		t.Fatalf("verify: %d %s %s", code, out, stderr)
	}
	k, _ := knowledge.Load(v.path)
	k.Plan.AppService = "worker"
	if err := k.Save(v.path); err != nil {
		t.Fatal(err)
	}
	start()
	log, _ := os.ReadFile(logPath)
	if strings.Count(string(log), "install-packages") != 1 {
		t.Fatalf("existing data was forced to reinstall:\n%s", log)
	}
	if _, stderr, code := v.verify(t); code == 0 || !strings.Contains(stderr, "execution evidence") {
		t.Fatalf("install verified for a service it never ran in: %d %s", code, stderr)
	}
}

func TestVerifyRefusesWhenANewerRunFailsDuringTheProbe(t *testing.T) {
	v, start, _ := oneOffVerify(t, []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
	}, "")
	start()
	afterVerifyProbe = func() {
		afterVerifyProbe = nil
		t.Setenv("DOCKER_FAKE_UP_MSG", "boom")
		t.Setenv("DOCKER_FAKE_UP_ALWAYS", "1")
		runCLI(t, append(append([]string{}, v.flags...), "start-env", v.root)...)
	}
	t.Cleanup(func() { afterVerifyProbe = nil })
	if _, stderr, code := v.verify(t); code == 0 || !strings.Contains(stderr, "newer start-env run") {
		t.Fatalf("verified after a newer run failed: %d %s", code, stderr)
	}
	if k, _ := knowledge.Load(v.path); k.Status == knowledge.StatusVerified {
		t.Fatal("knowledge was written as verified")
	}
}

func TestStandaloneSeedRetryEstablishesExecutionEvidence(t *testing.T) {
	v, start, logPath := oneOffVerify(t, []knowledge.Step{
		{ID: "dependencies", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}},
		{ID: "seed", Kind: knowledge.KindExec, On: knowledge.OnOneOff, Service: "app", Cmd: "seed-data", Once: knowledge.OnceData},
		{ID: "up", Kind: knowledge.KindComposeUp},
	}, "")
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "1")
	if _, _, code := runCLI(t, append(append([]string{}, v.flags...), "start-env", v.root)...); code == 0 {
		t.Fatal("start-env should fail in the seed")
	}
	t.Setenv("DOCKER_FAKE_EXEC_EXIT", "0")
	if out, stderr, code := runCLI(t, append(append([]string{}, v.flags...), "--root", v.root, "seed")...); code != 0 {
		t.Fatalf("seed retry: %d %s %s", code, out, stderr)
	}
	start()
	log, _ := os.ReadFile(logPath)
	if strings.Count(string(log), "sh -c seed-data") != 2 {
		t.Fatalf("expected the failed attempt and one retry only:\n%s", log)
	}
	if out, stderr, code := v.verify(t); code != 0 {
		t.Fatalf("a successful seed retry gave no evidence: %d %s %s", code, out, stderr)
	}
}

// preflightMutator calls change the first time the secrets preflight reports a
// resolved value: the moment a recipe on disk can differ from the plan the
// command captured.
type preflightMutator struct {
	change func()
	called bool
}

func (w *preflightMutator) Write(data []byte) (int, error) {
	if !w.called && strings.Contains(string(data), "resolves") {
		w.called = true
		w.change()
	}
	return len(data), nil
}

// The seed command is read from the recipe on disk, after the plan is captured.
// When the two differ, what ran must not count as the captured task, even under
// a hash a Start declared early. A prefix runs as its own command, so the
// captured command still runs after one that exits 0, and that counts.
func TestSeedEvidenceNeedsTheCapturedCommandToRun(t *testing.T) {
	for _, tc := range []struct {
		name, changed string
		ran           []string
		verified      bool
		args          func(repo string) []string
		env           map[string]string
	}{
		{name: "standalone seed with an exiting prefix", changed: "exit 0 && seed-original", ran: []string{"sh -c exit 0", "sh -c seed-original"}, verified: true,
			args: func(repo string) []string { return []string{"--root", repo, "seed"} }},
		{name: "start with a declared hash", changed: "seed-other", ran: []string{"sh -c seed-other"},
			args: func(repo string) []string { return []string{"start-env", repo} },
			env:  map[string]string{"DOCKER_FAKE_VOLUME_EXIT": "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, base, logPath, _ := preflightFixture(t, knowledge.SecretDecl{Name: "NPM_TOKEN"})
			fakeOp(t, "seed-evidence-canary")
			mapSecret(t, repo, "NPM_TOKEN", "op://test/item/token")
			compose := "services:\n  db:\n    image: postgres:17\n    volumes: [data:/data]\n    secrets: [NPM_TOKEN]\nvolumes:\n  data: {}\nsecrets:\n  NPM_TOKEN:\n    environment: NPM_TOKEN\n"
			if err := os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(compose), 0o600); err != nil {
				t.Fatal(err)
			}
			path := knowledgePath(repo)
			original, err := knowledge.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			original.Entry = &knowledge.Entry{None: true}
			original.Plan = knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
				{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", ".agent-dev/app.compose.yaml"}},
				{ID: "up", Kind: knowledge.KindComposeUp},
				{ID: "seed", Kind: knowledge.KindExec, Service: "db", On: knowledge.OnOneOff, Cmd: "seed-original", Once: knowledge.OnceData},
			}}
			if err := verifiedFixture(original).Save(path); err != nil {
				t.Fatal(err)
			}
			if _, stderr, code := runCLI(t, append(append([]string{}, base...), "--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml")...); code != 0 {
				t.Fatalf("prepare: %s", stderr)
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			w := &preflightMutator{change: func() {
				changed, err := knowledge.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				changed.Plan.Steps[2].Cmd = tc.changed
				if err := changed.Save(path); err != nil {
					t.Fatal(err)
				}
			}}
			var stderr strings.Builder
			if code := Run(append(append([]string{}, base...), tc.args(repo)...), w, &stderr); code != 0 || !w.called {
				t.Fatalf("run: code %d, changed %t: %s", code, w.called, stderr.String())
			}
			for k := range tc.env {
				os.Unsetenv(k)
			}
			if err := original.Save(path); err != nil {
				t.Fatal(err)
			}
			if _, stderr, code := runCLI(t, append(append([]string{}, base...), "start-env", repo)...); code != 0 {
				t.Fatal(stderr)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, ran := range tc.ran {
				if !strings.Contains(string(log), ran) {
					t.Fatalf("%q did not run:\n%s", ran, log)
				}
			}
			if !tc.verified && strings.Contains(string(log), "sh -c seed-original") {
				t.Fatalf("seed-original ran:\n%s", log)
			}
			v := recipeVerify{root: repo, flags: base, path: path}
			if _, stderr, code := v.verify(t); (code == 0) != tc.verified {
				t.Fatalf("verify code %d, want verified %t (ran %q): %s", code, tc.verified, tc.ran, stderr)
			}
		})
	}
}
