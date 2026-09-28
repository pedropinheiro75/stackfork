package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// learnFixtureRoot writes a minimal real Compose project so `stackfork
// learn` has something to scan.
func learnFixtureRoot(t *testing.T) string {
	t.Helper()
	root := initRepo(t)
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestLearnRecordRequiresPriorScan covers task 13.5: nothing to record
// into without a scan first.
func TestLearnRecordRequiresPriorScan(t *testing.T) {
	root := initRepo(t)
	_, stderr, code := runCLI(t, "--root", root, "learn", "record", "answer", "q", "a")
	if code == 0 || !strings.Contains(stderr, "run `stackfork learn` first") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

// TestLearnRecordAnswerGotchaRecipe covers task 13.5: each kind appends/
// sets correctly and persists to knowledge.json.
func TestLearnRecordAnswerGotchaRecipe(t *testing.T) {
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
	if k.Recipe.Prepare != "stackfork prepare -f compose.yaml" || k.Recipe.Up != "stackfork up" {
		t.Fatalf("unexpected recipe: %+v", k.Recipe)
	}
	if len(k.Recipe.Login) != 2 {
		t.Fatalf("unexpected login steps: %+v", k.Recipe.Login)
	}
	if k.Status != knowledge.StatusDraft {
		t.Fatalf("expected recording to leave status as draft, got %q", k.Status)
	}
}

// TestLearnScanResumesDraft covers task 13.5: re-running learn after
// recording answers/gotchas/a recipe must not discard them, only refresh
// Facts/Refusals/Risks/EnvKeys/Inputs.
func TestLearnScanResumesDraft(t *testing.T) {
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	runCLI(t, "--root", root, "learn", "record", "answer", "q", "a")
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "p", "--up", "u")

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
	if len(k.Answers) != 1 || k.Recipe.Prepare != "p" {
		t.Fatalf("expected the recorded answer/recipe to survive a re-scan, got answers=%+v recipe=%+v", k.Answers, k.Recipe)
	}
}

// TestLearnVerifyRequiresRecordedRecipe covers task 13.6's refusal.
func TestLearnVerifyRequiresRecordedRecipe(t *testing.T) {
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")

	_, stderr, code := runCLI(t, "--root", root, "learn", "verify", "--check", "db up", "--confirm", "it works")
	if code == 0 || !strings.Contains(stderr, "recipe is incomplete") {
		t.Fatalf("expected a refusal for an unrecorded recipe: code=%d stderr=%s", code, stderr)
	}
}

// TestLearnVerifySetsStatus covers task 13.6: verify moves draft to
// verified with a date, the checks and the user's own quoted confirmation.
func TestLearnVerifySetsStatus(t *testing.T) {
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "stackfork prepare -f compose.yaml", "--up", "stackfork up")

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
	root := learnFixtureRoot(t)
	runCLI(t, "--root", root, "learn")
	runCLI(t, "--root", root, "learn", "record", "recipe", "--prepare", "p", "--up", "u")
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
