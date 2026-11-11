package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const draftSeedNote = "seed not run: setup is a draft; record it with `stackfork learn record step --id seed --kind exec --once data --cmd …` (or finish `stackfork learn verify`)"

func saveKnowledge(t *testing.T, root string, k knowledge.Knowledge) {
	t.Helper()
	k.Schema = knowledge.CurrentSchema
	if err := verifiedFixture(&k).Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
}

// A draft checkout (learn scanned it, nobody recorded a seed): up must not
// run the seed script it only guessed.
func TestDraftUpRunsNoAutoDetectedSeed(t *testing.T) {
	root, stateDir, dir, log := autoSeedFixture(t)
	saveKnowledge(t, root, knowledge.Knowledge{Status: knowledge.StatusDraft})
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up")
	b, _ := os.ReadFile(log)
	if code != 0 || strings.Contains(string(b), " exec ") || !strings.Contains(out+stderr, draftSeedNote) {
		t.Fatalf("up: %d %s %s\n%s", code, out, stderr, b)
	}
	if m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json")); m == nil || (m.Seeded != nil && *m.Seeded) {
		t.Fatalf("seeded flag must stay false: %+v", m)
	}
}

// start-env on a draft with a proposed plan: neither the proposed migrate nor
// the proposed seed runs; both run steps are skipped with the reason.
func TestDraftStartEnvSkipsProposedMigrateAndSeed(t *testing.T) {
	root, stateDir := reposEnv(t)
	repo := planRepo(t, root, nil)
	writeFile(t, filepath.Join(repo, "package.json"), `{"scripts":{"db:migrate":"m","db:seed":"s"}}`) // what learn detected
	saveKnowledge(t, repo, knowledge.Knowledge{Status: knowledge.StatusDraft, Plan: knowledge.Plan{Status: knowledge.PlanProposed, Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "m"},
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "db", Cmd: "s"},
	}}})
	log := installFakeDocker(t)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	base := []string{"--state-dir", stateDir, "--legacy-state-dir", t.TempDir()}
	if _, e, code := runCLI(t, append(base, "--root", repo, "prepare", "-f", ".agent-dev/app.compose.yaml")...); code != 0 {
		t.Fatal(e)
	}
	out, e, code := runCLI(t, append(base, "start-env", repo)...)
	if b := string(mustRead(t, log)); code != 0 || strings.Contains(b, " exec ") {
		t.Fatalf("%d %s %s\n%s", code, out, e, b)
	}
	if !strings.Contains(out+e, draftSeedNote) {
		t.Errorf("no note: %s %s", out, e)
	}
	dirs, _ := filepath.Glob(filepath.Join(stateDir, "*"))
	var latest *runs.Run
	for _, d := range dirs {
		if r := runs.Latest(d); r != nil {
			latest = r
		}
	}
	if latest == nil {
		t.Fatal("no run recorded")
	}
	got := map[string]string{}
	for _, s := range latest.Steps {
		if s.State == runs.Skipped {
			got[s.ID] = s.Reason
		}
	}
	if !strings.Contains(got["seed"], "seed not run: setup is a draft") || !strings.Contains(got["migrate"], "migrate not run: setup is a draft") {
		t.Fatalf("skipped steps: %v", got)
	}
}

func TestDraftWithARecordedSeedStillSeeds(t *testing.T) {
	root, stateDir, _, log := autoSeedFixture(t)
	writeKnowledgeJSON(t, root, `{"schema":2,"status":"draft","plan":{"status":"recorded","steps":[{"id":"up","kind":"compose-up"},{"id":"seed","kind":"exec","service":"db","cmd":"bun run db:seed","once":"data"}]}}`)
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "bun run db:seed") {
		t.Fatalf("recorded seed did not run:\n%s", b)
	}
}

func TestVerifiedAutoDetectedSeedStillRuns(t *testing.T) {
	root, stateDir, _, log := autoSeedFixture(t)
	saveKnowledge(t, root, knowledge.Knowledge{Status: knowledge.StatusVerified})
	t.Setenv("DOCKER_FAKE_VOLUME_EXIT", "1")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "npm run db:seed") {
		t.Fatalf("verified auto-detected seed did not run:\n%s", b)
	}
}

// The derived plan shows the reason a Start will print.
func TestDeriveDraftSeedShowsTheSkipReason(t *testing.T) {
	f := newDeriveFix(t)
	f.write(t, f.repo, "docker-compose.yml", "services:\n  app:\n    image: alpine:3.20\n")
	f.seedable(t)
	f.know(t, f.repo, knowledge.Knowledge{Status: knowledge.StatusDraft})
	if _, e, code := runCLI(t, append(f.base(), "--root", f.repo, "prepare", "--force", "-f", "docker-compose.yml")...); code != 0 {
		t.Fatal(e)
	}
	d, err := f.derive(t, f.repo, "")
	if err != nil || d.Refusal != "" {
		t.Fatalf("%+v %v", d, err)
	}
	var seed lifecycle.PlanStep
	for _, s := range d.Steps {
		if s.ID == "seed" {
			seed = s
		}
	}
	if !strings.Contains(seed.Source, draftSeedNote) {
		t.Fatalf("seed source %q", seed.Source)
	}
}
