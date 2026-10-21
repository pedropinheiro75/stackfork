package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// learnFixtureRoot writes a minimal real Compose project so `stackfork
// learn` has something to scan.
func learnFixtureRoot(t *testing.T) string {
	t.Helper()
	root := initBareRepo(t)
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestLearnRecordRequiresPriorScan covers task 13.5: nothing to record
// into without a scan first.
func TestLearnRecordRequiresPriorScan(t *testing.T) {
	root := initBareRepo(t)
	_, stderr, code := runCLI(t, "--root", root, "learn", "record", "answer", "q", "a")
	if code == 0 || !strings.Contains(stderr, "run `stackfork learn` first") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

// TestLearnRecordAnswerGotchaRecipe covers task 13.5: each kind appends/
// sets correctly and persists to knowledge.json.
func TestLearnRecordAnswerGotchaRecipe(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	if _, stderr, code := runCLI(t, "--root", root, "learn"); code != 0 {
		t.Fatalf("scan failed: %s", stderr)
	}

	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "answer", "which seed dataset?", "the minimal one"); code != 0 {
		t.Fatalf("record answer failed: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "gotcha", "db never came up", "add a healthcheck"); code != 0 {
		t.Fatalf("record gotcha failed: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "recipe",
		"--prepare", "stackfork prepare -f compose.yaml",
		"--up", "stackfork up",
		"--login", "visit /login",
		"--login", "use the seeded admin"); code != 0 {
		t.Fatalf("record recipe failed: %s", stderr)
	}

	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Answers) != 1 || k.Answers[0].Question != "which seed dataset?" || k.Answers[0].Answer != "the minimal one" {
		t.Fatalf("unexpected answers: %+v", k.Answers)
	}
	if len(k.Gotchas) != 1 || k.Gotchas[0].Problem != "db never came up" {
		t.Fatalf("unexpected gotchas: %+v", k.Gotchas)
	}
	if !reflect.DeepEqual(stepArgs(k, knowledge.KindPrepare), []string{"-f", "compose.yaml"}) || k.Plan.Status != knowledge.PlanRecorded {
		t.Fatalf("unexpected plan: %+v", k.Plan)
	}
	notes := 0
	for _, s := range k.Plan.Steps {
		if s.Kind == knowledge.KindNote {
			notes++
		}
	}
	if notes != 2 {
		t.Fatalf("unexpected login notes: %+v", k.Plan.Steps)
	}
	if k.Status != knowledge.StatusDraft {
		t.Fatalf("expected recording to leave status as draft, got %q", k.Status)
	}
}

// TestLearnScanResumesDraft covers task 13.5: re-running learn after
// recording answers/gotchas/a recipe must not discard them, only refresh
// Facts/Refusals/Risks/EnvKeys/Inputs.
func TestLearnScanResumesDraft(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	runCLI(t, "--root", root, "learn", "record", "answer", "q", "a")
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "p", "--up", "u")
	before, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}

	out, stderr, code := runCLI(t, "--root", root, "learn")
	if code != 0 {
		t.Fatalf("re-scan failed: %s", stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if resumed, _ := result["resumed"].(bool); !resumed {
		t.Fatalf("expected resumed=true, got %s", out)
	}

	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Answers) != 1 || k.Plan.Status != knowledge.PlanRecorded {
		t.Fatalf("expected the recorded answer/plan to survive a re-scan, got answers=%+v plan=%+v", k.Answers, k.Plan)
	}
	if !reflect.DeepEqual(k.Plan, before.Plan) {
		t.Fatalf("recorded plan changed by re-scan:\nbefore %+v\nafter  %+v", before.Plan, k.Plan)
	}
}

// A proposed plan is replaced by the fresh proposal on re-scan.
func TestLearnScanReplacesProposedPlan(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	k, _ := knowledge.Load(path)
	k.Plan.Steps = append(k.Plan.Steps, knowledge.Step{ID: "stale", Kind: knowledge.KindNote, Text: "x"})
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	runCLI(t, "--root", root, "learn")
	k, _ = knowledge.Load(path)
	for _, s := range k.Plan.Steps {
		if s.ID == "stale" {
			t.Fatal("a proposed plan must be replaced by the fresh proposal")
		}
	}
}

func stepArgs(k *knowledge.Knowledge, kind string) []string {
	for _, s := range k.Plan.Steps {
		if s.Kind == kind {
			return s.Args
		}
	}
	return nil
}

func TestLearnRecordStepUpsertsAndSetsRecorded(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	load := func() *knowledge.Knowledge {
		k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	find := func(k *knowledge.Knowledge, id string) (n int, s knowledge.Step) {
		for _, x := range k.Plan.Steps {
			if x.ID == id {
				n++
				s = x
			}
		}
		return
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "step", "--id", "app", "--kind", "host", "--cmd", "bun run dev", "--modes", "hybrid", "--name", "app"); code != 0 {
		t.Fatalf("step: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "step", "--id", "app", "--kind", "host", "--cmd", "bun run dev --port 3000"); code != 0 {
		t.Fatalf("step: %s", stderr)
	}
	k := load()
	if n, s := find(k, "app"); n != 1 || s.Cmd != "bun run dev --port 3000" || k.Plan.Status != knowledge.PlanRecorded {
		t.Fatalf("upsert: %d %+v %s", n, s, k.Plan.Status)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "step", "--remove", "app"); code != 0 {
		t.Fatalf("remove: %s", stderr)
	}
	if n, _ := find(load(), "app"); n != 0 {
		t.Fatal("remove failed")
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "step", "--id", "first", "--kind", "note", "--text", "hi", "--before", "up"); code != 0 {
		t.Fatalf("before: %s", stderr)
	}
	ids := []string{}
	for _, s := range load().Plan.Steps {
		ids = append(ids, s.ID)
	}
	fi, ui := -1, -1
	for i, id := range ids {
		if id == "first" {
			fi = i
		}
		if id == "up" {
			ui = i
		}
	}
	if fi < 0 || fi+1 != ui {
		t.Fatalf("--before up: %v", ids)
	}
	_, stderr, code := runCLI(t, "--root", root, "learn", "record", "step", "--id", "w", "--kind", "wait-http")
	if code != 1 || !strings.Contains(stderr, "wait-http") {
		t.Fatalf("wait-http: %d %s", code, stderr)
	}
}

func TestLearnRecordRecipeAliasWritesSteps(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "recipe", "--migrate", "m", "--seed-once", "s", "--seed-service", "web"); code != 0 {
		t.Fatalf("recipe: %s", stderr)
	}
	k, _ := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if m := k.Plan.MigrateStep(); m == nil || m.Cmd != "m" {
		t.Fatalf("migrate %+v", m)
	}
	if s := k.Plan.SeedStep(); s == nil || s.Cmd != "s" || s.Service != "web" {
		t.Fatalf("seed %+v", s)
	}
}

// TestLearnVerifyRequiresRecordedPlan covers task 13.6's refusal.
func TestLearnVerifyRequiresRecordedPlan(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")

	_, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "db up", "--confirm", "it works")
	if code == 0 || !strings.Contains(stderr, "learn record step") {
		t.Fatalf("expected a refusal for a proposed plan: code=%d stderr=%s", code, stderr)
	}
	runCLI(t, "--root", root, "learn", "record", "step", "--id", "n", "--kind", "note", "--text", "x")
	runCLI(t, "--root", root, "learn", "record", "entry", "--none")
	prepareForVerify(t, root)
	if _, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "y"); code != 0 {
		t.Fatalf("recorded plan with compose-up must verify: %s", stderr)
	}
	runCLI(t, "--root", root, "learn", "record", "step", "--remove", "up")
	if _, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "y"); code == 0 || !strings.Contains(stderr, "compose-up") {
		t.Fatalf("no compose-up must fail: %d %s", code, stderr)
	}
}

// TestLearnVerifySetsStatus covers task 13.6: verify moves draft to
// verified with a date, the checks and the user's own quoted confirmation.
func TestLearnVerifySetsStatus(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "stackfork prepare -f compose.yaml", "--up", "stackfork up")
	runCLI(t, "--root", root, "learn", "record", "entry", "--none")
	prepareForVerify(t, root)

	_, stderr, code := runCLI(t, "--root", root, "learn", "verify",
		"--check", "database identity", "--check", "app readiness",
		"--confirm", "yes, the app loads and I can log in")
	if code != 0 {
		t.Fatalf("verify failed: %s", stderr)
	}

	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if k.Status != knowledge.StatusVerified {
		t.Fatalf("expected verified status, got %q", k.Status)
	}
	if k.Verification == nil || k.Verification.Confirmation != "yes, the app loads and I can log in" {
		t.Fatalf("unexpected verification: %+v", k.Verification)
	}
	if len(k.Verification.Checks) != 2 || k.Verification.Date == "" {
		t.Fatalf("unexpected verification: %+v", k.Verification)
	}
}

// TestWhereReportsStaleKnowledge covers task 13.7: `where` reports
// verified-but-stale knowledge without rewriting the file.
func TestWhereReportsStaleKnowledge(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "p", "--up", "u")
	runCLI(t, "--root", root, "learn", "record", "entry", "--none")
	prepareForVerify(t, root)
	runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "yes")

	// Change the scanned input after verifying.
	composePath := filepath.Join(root, "compose.yaml")
	data, _ := os.ReadFile(composePath)
	os.WriteFile(composePath, append(data, []byte("\n# changed\n")...), 0o600)

	out, stderr, code := runCLI(t, "--root", root, "where")
	if code != 0 {
		t.Fatalf("where failed: %s", stderr)
	}
	var result struct {
		Knowledge struct {
			Status       string   `json:"status"`
			ChangedFiles []string `json:"changed_files"`
		} `json:"knowledge"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Knowledge.Status != knowledge.StatusStale {
		t.Fatalf("expected where to report stale knowledge, got %q\n%s", result.Knowledge.Status, out)
	}
	if len(result.Knowledge.ChangedFiles) == 0 {
		t.Fatalf("expected changed_files to name the edited input:\n%s", out)
	}

	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if k.Status != knowledge.StatusVerified {
		t.Fatalf("expected the file itself to stay verified (staleness is reported, not rewritten), got %q", k.Status)
	}
}

// A schema 1 recipe with seed_pending still converts to a recorded plan:
// re-running learn keeps the recorded prepare args and migrate.
func TestLearnKeepsConvertedSchema1PlanWithSeedPending(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	writeKnowledgeJSON(t, root, `{"schema":1,"recipe":{"prepare":"stackfork prepare -f compose.yaml --env-file .env.dev","migrate":"custom-migrate","seed_pending":true}}`)
	if _, stderr, code := runCLI(t, "--root", root, "learn"); code != 0 {
		t.Fatal(stderr)
	}
	k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(stepArgs(k, knowledge.KindPrepare), " "); got != "-f compose.yaml --env-file .env.dev" {
		t.Fatalf("prepare args lost: %q", got)
	}
	if m := k.Plan.MigrateStep(); m == nil || m.Cmd != "custom-migrate" {
		t.Fatalf("migrate lost: %+v", k.Plan.Steps)
	}
	if k.Plan.NoSeed || k.Plan.SeedStep() != nil {
		t.Fatalf("seeding must stay undecided: %+v", k.Plan)
	}
	runCLI(t, "--root", root, "learn", "record", "entry", "--none")
	prepareForVerify(t, root)
	if _, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "c", "--confirm", "y"); code != 0 {
		t.Fatalf("verify: %s", stderr)
	}
}

func TestLearnRecordStepPortAndArgs(t *testing.T) {
	testsupport.RequireCompose(t)
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "step", "--id", "app", "--kind", "host", "--name", "app", "--cmd", "bun run dev", "--port", "3000"); code != 0 {
		t.Fatalf("port: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "--root", root, "learn", "record", "step", "--id", "prepare", "--kind", "prepare", "--args", "-f,compose.yaml"); code != 0 {
		t.Fatalf("args: %s", stderr)
	}
	k, _ := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
	var gotPort int
	for _, s := range k.Plan.Steps {
		if s.ID == "app" {
			gotPort = s.Port
		}
	}
	if gotPort != 3000 || strings.Join(stepArgs(k, knowledge.KindPrepare), " ") != "-f compose.yaml" {
		t.Fatalf("port %d, args %v", gotPort, stepArgs(k, knowledge.KindPrepare))
	}
	for _, bad := range [][]string{
		{"--id", "a", "--kind", "host", "--name", "a", "--cmd", "x", "--port", "70000"},
		{"--id", "a", "--kind", "note", "--text", "t", "--port", "3000"},
		{"--id", "a", "--kind", "note", "--text", "t", "--args", "x"},
	} {
		if _, _, code := runCLI(t, append([]string{"--root", root, "learn", "record", "step"}, bad...)...); code == 0 {
			t.Errorf("%v must be refused", bad)
		}
	}
}

// entry-links task 3.1: a re-scan keeps a recorded entry, `none` and the
// links, and replaces a proposed entry with the fresh proposal.
func TestLearnScanEntryMerge(t *testing.T) {
	testsupport.RequireCompose(t)
	root := initRepo(t)
	web := "services:\n  web:\n    image: nginx\n    ports:\n      - \"3000:3000\"\n  db:\n    image: postgres:17\n"
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(web), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".stackfork", "knowledge.json")
	scan := func() *knowledge.Knowledge {
		t.Helper()
		if _, stderr, code := runCLI(t, "--root", root, "learn"); code != 0 {
			t.Fatal(stderr)
		}
		k, err := knowledge.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	if k := scan(); k.Entry == nil || k.Entry.Service != "web" || k.Entry.Port != 3000 || !k.Entry.Proposed {
		t.Fatalf("%+v", k.Entry)
	}
	// A proposed entry that no longer matches is replaced.
	k, _ := knowledge.Load(path)
	k.Entry = &knowledge.Entry{Service: "old", Port: 1, Proposed: true}
	k.Save(path)
	if k := scan(); k.Entry.Service != "web" {
		t.Fatalf("%+v", k.Entry)
	}
	// Recorded entry and links survive.
	k, _ = knowledge.Load(path)
	k.Entry = &knowledge.Entry{Service: "web", Port: 3000, Path: "/app"}
	k.Links = []knowledge.Link{{Name: "Dashboard", Path: "/admin"}, {Name: "Email", Service: "db", Port: 5432}}
	k.Save(path)
	k2 := scan()
	if k2.Entry == nil || *k2.Entry != *k.Entry || !reflect.DeepEqual(k2.Links, k.Links) {
		t.Fatalf("%+v %+v", k2.Entry, k2.Links)
	}
	k.Entry = &knowledge.Entry{None: true}
	k.Save(path)
	if k3 := scan(); k3.Entry == nil || !k3.Entry.None {
		t.Fatalf("%+v", k3.Entry)
	}
}
