package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

const (
	gapBase     = "services:\n  app:\n    image: alpine:3.20\n"
	gapOverride = "services:\n  app:\n    image: alpine:3.20\n    environment:\n      MODE: dev\n"
)

// gapRepo is the improve-worktree-recipes fixture: a main checkout `acme-web`
// with a tracked base file and a tracked override, prepared with both, and a
// linked worktree feat-a that has not been prepared. A verified knowledge
// file with no plan lets start-env run; with no plan step the worktree's
// prepare rule 3 picks the override alone (the short recipe).
func gapRepo(t *testing.T) (repo, wt string, base []string) {
	t.Helper()
	root, state := reposEnv(t)
	repo = scratchRepo(t, filepath.Join(root, "acme-web"))
	os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(repo, "compose.yaml"), []byte(gapBase), 0o644)
	os.WriteFile(filepath.Join(repo, ".stackfork", "dev.compose.yaml"), []byte(gapOverride), 0o644)
	for _, args := range [][]string{{"add", "."}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "compose"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	base = []string{"--state-dir", state, "--legacy-state-dir", t.TempDir()}
	if out, e, code := runCLI(t, append(append([]string{}, base...), "--root", repo, "prepare", "-f", "compose.yaml", "-f", ".stackfork/dev.compose.yaml")...); code != 0 {
		t.Fatalf("prepare main: %s %s", out, e)
	}
	if err := (&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	if out, e, code := runCLI(t, append(append([]string{}, base...), "worktree", "add", repo, "feat-a", "--from", "main")...); code != 0 {
		t.Fatalf("worktree add: %s %s", out, e)
	}
	wt = filepath.Join(repo, ".worktrees", "feat-a")
	return
}

func gapWarning(wt string) string {
	return "warning: this worktree prepares with 1 Compose file (.stackfork/dev.compose.yaml); the main checkout's last prepare used 2 (compose.yaml, .stackfork/dev.compose.yaml). Missing: compose.yaml. To reuse the main checkout's recipe: stackfork --root " + wt + " reset --from-main"
}

func TestPrepareWarnsOnceOnARecipeGap(t *testing.T) {
	_, wt, base := gapRepo(t)
	out, e, code := runCLI(t, append(append([]string{}, base...), "--root", wt, "prepare", "-f", ".stackfork/dev.compose.yaml")...)
	if code != 0 {
		t.Fatalf("a gap must not fail prepare: %d %s %s", code, out, e)
	}
	if strings.Count(e, gapWarning(wt)) != 1 {
		t.Fatalf("want the gap warning once in stderr:\n%s", e)
	}
	// both files: no warning
	_, e, code = runCLI(t, append(append([]string{}, base...), "--root", wt, "prepare", "-f", "compose.yaml", "-f", ".stackfork/dev.compose.yaml")...)
	if code != 0 || strings.Contains(e, "Missing:") {
		t.Fatalf("complete recipe warned: %d %s", code, e)
	}
}

func TestStartEnvPrepareStepLogHoldsTheGap(t *testing.T) {
	repo, wt, base := gapRepo(t)
	_ = repo
	out, e, code := runCLI(t, append(append([]string{}, base...), "start-env", wt)...)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if !strings.Contains(out, gapWarning(wt)) {
		t.Fatalf("progress output lacks the gap:\n%s", out)
	}
	dirs, _ := filepath.Glob(filepath.Join(base[1], "*", "runs"))
	var found bool
	for _, d := range dirs {
		inst := filepath.Dir(d)
		m, _ := os.ReadFile(filepath.Join(inst, "manifest.json"))
		if !strings.Contains(string(m), "feat-a") {
			continue
		}
		b, err := runs.Log(inst, 1, "prepare")
		if err != nil || !strings.Contains(string(b), "Missing: compose.yaml") {
			t.Fatalf("prepare step log: %q %v", b, err)
		}
		found = true
	}
	if !found {
		t.Fatal("no run for the worktree")
	}
}

func TestResetPreviewDescribesTheGap(t *testing.T) {
	_, wt, base := gapRepo(t)
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", wt, "prepare", "-f", ".stackfork/dev.compose.yaml")...); code != 0 {
		t.Fatal(e)
	}
	pv, err := PreviewReset(wt, base[1], base[3], false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pv.Describe(), gapWarning(pv.Root)) {
		t.Fatalf("preview lacks the gap:\n%s", pv.Describe())
	}
}

func listWarnings(t *testing.T, base []string) map[string][]string {
	t.Helper()
	out, e, code := runCLI(t, append(append([]string{}, base...), "list", "--json")...)
	if code != 0 {
		t.Fatalf("%s", e)
	}
	var inv struct {
		Instances []struct {
			Root     string   `json:"root"`
			Warnings []string `json:"warnings"`
		} `json:"instances"`
	}
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		t.Fatal(err)
	}
	m := map[string][]string{}
	for _, i := range inv.Instances {
		m[i.Root] = i.Warnings
	}
	return m
}

func TestListWarnsOnlyForLinkedWorktreeWithAGap(t *testing.T) {
	repo, wt, base := gapRepo(t)
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", wt, "prepare", "-f", ".stackfork/dev.compose.yaml")...); code != 0 {
		t.Fatal(e)
	}
	raw, _, _ := runCLI(t, append(append([]string{}, base...), "list", "--json")...)
	if strings.Count(raw, `"recipe_gap": true`) != 1 || strings.Count(raw, `"from_main": true`) != 1 {
		t.Fatalf("only the worktree carries recipe_gap and from_main:\n%s", raw)
	}
	w := listWarnings(t, base)
	var gotWt []string
	for root, ws := range w {
		if strings.HasSuffix(filepath.ToSlash(root), "/.worktrees/feat-a") {
			gotWt = ws
		} else if root == repo && strings.Contains(strings.Join(ws, "\n"), "Missing:") {
			t.Fatalf("the main checkout's instance warned: %v", ws)
		}
	}
	if len(gotWt) != 1 || gotWt[0] != gapWarning(wt) {
		t.Fatalf("worktree warnings %v", gotWt)
	}
	// no reference: a worktree of a repo whose main was never prepared stays quiet
	root2, state2 := reposEnv(t)
	repo2 := scratchRepo(t, filepath.Join(root2, "solo"))
	os.WriteFile(filepath.Join(repo2, "a.yaml"), []byte(gapBase), 0o644)
	b2 := []string{"--state-dir", state2, "--legacy-state-dir", t.TempDir()}
	runCLI(t, append(append([]string{}, b2...), "worktree", "add", repo2, "x", "--from", "main")...)
	wt2 := filepath.Join(repo2, ".worktrees", "x")
	os.WriteFile(filepath.Join(wt2, "a.yaml"), []byte(gapBase), 0o644)
	if _, e, code := runCLI(t, append(append([]string{}, b2...), "--root", wt2, "prepare", "--force", "-f", "a.yaml")...); code != 0 || strings.Contains(e, "Missing:") {
		t.Fatalf("%d %s", code, e)
	}
	for _, ws := range listWarnings(t, b2) {
		if strings.Contains(strings.Join(ws, "\n"), "Missing:") {
			t.Fatalf("gap without a reference: %v", ws)
		}
	}
}

// M1: the reset-from-main offer belongs to the instance of the branch that
// is checked out in the worktree now.
func TestListOffersResetFromMainOnlyForTheCheckedOutBranch(t *testing.T) {
	_, wt, base := gapRepo(t)
	if _, e, code := runCLI(t, append(append([]string{}, base...), "--root", wt, "prepare", "-f", ".stackfork/dev.compose.yaml")...); code != 0 {
		t.Fatal(e)
	}
	raw, _, _ := runCLI(t, append(append([]string{}, base...), "list", "--json")...)
	if strings.Count(raw, `"from_main": true`) != 1 {
		t.Fatalf("the checked-out branch's instance must offer it:\n%s", raw)
	}
	if out, err := exec.Command("git", "-C", wt, "checkout", "-q", "-b", "other").CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	raw, _, _ = runCLI(t, append(append([]string{}, base...), "list", "--json")...)
	if strings.Contains(raw, `"from_main": true`) || strings.Contains(raw, `"recipe_gap": true`) {
		t.Fatalf("the instance of another branch must not offer it:\n%s", raw)
	}
}
