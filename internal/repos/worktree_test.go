package repos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func mkWithOrigin(t *testing.T) (repo, origin string) {
	t.Helper()
	root := tempRoot(t)
	origin = filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	repo = mkRepo(t, filepath.Join(root, "proj"))
	git(t, repo, "remote", "add", "origin", origin)
	git(t, repo, "push", "-q", "-u", "origin", "main")
	return
}

func TestAddWorktreeLocalRemoteOnlyNewAndRefusals(t *testing.T) {
	repo, origin := mkWithOrigin(t)

	// remote-only branch: push from a second clone, fetch, no local branch
	clone := filepath.Join(tempRoot(t), "clone")
	git(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	git(t, clone, "checkout", "-q", "-b", "feature/remote")
	os.WriteFile(filepath.Join(clone, "r"), []byte("r"), 0o644)
	git(t, clone, "add", "r")
	git(t, clone, "commit", "-q", "-m", "r")
	git(t, clone, "push", "-q", "origin", "feature/remote")
	git(t, repo, "fetch", "-q")

	res, err := AddWorktree(repo, "feature/remote", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, ".worktrees", "feature-remote"); res.Path != want {
		t.Fatalf("path %s", res.Path)
	}
	if b := strings.TrimSpace(git(t, res.Path, "rev-parse", "--abbrev-ref", "HEAD")); b != "feature/remote" {
		t.Fatalf("branch %s", b)
	}
	if up := strings.TrimSpace(git(t, res.Path, "rev-parse", "--abbrev-ref", "feature/remote@{upstream}")); up != "origin/feature/remote" {
		t.Fatalf("upstream %q", up)
	}

	// existing local branch
	git(t, repo, "branch", "local-one")
	if _, err := AddWorktree(repo, "local-one", ""); err != nil {
		t.Fatal(err)
	}

	// existing target refuses and changes nothing
	if _, err := AddWorktree(repo, "local-one", ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v", err)
	}
	// unknown branch needs --from; with --from it is created
	if _, err := AddWorktree(repo, "brand-new", ""); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("got %v", err)
	}
	if _, err := AddWorktree(repo, "brand-new", "main"); err != nil {
		t.Fatal(err)
	}
	// bad names and bad --from
	for _, bad := range []string{"", "-x", "a..b", "a b", "x.lock"} {
		if _, err := AddWorktree(repo, bad, "main"); err == nil {
			t.Errorf("branch %q should be refused", bad)
		}
	}
	if _, err := AddWorktree(repo, "zzz", "--output=x"); err == nil {
		t.Error("option-like --from must be refused")
	}

	// exclude entry exactly once, and git status of main is clean of .worktrees
	ex, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Count(string(ex), "/.worktrees/") != 1 {
		t.Fatalf("exclude: %q", ex)
	}
	if st := git(t, repo, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Fatalf("main checkout dirtied: %q", st)
	}
	// a worktree folder that is a plain file target also refuses
	if _, err := AddWorktree(filepath.Join(repo, ".worktrees", "local-one"), "x", "main"); err == nil {
		t.Fatal("a linked worktree is not a main checkout")
	}
}

func TestAddWorktreeCopiesOnlyAllowedAdapterFiles(t *testing.T) {
	repo, _ := mkWithOrigin(t)
	w := func(rel, content string) {
		p := filepath.Join(repo, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	w(".agent-dev/app.compose.yaml", "services: {}")
	w(".agent-dev/runtime.env", "A=1")
	w(".agent-dev/.env", "SECRET=1")
	w(".agent-dev/.env.local", "SECRET=1")
	w(".agent-dev/tls.key", "k")
	w(".agent-dev/tls.pem", "k")
	w(".agent-dev/id_ed25519", "k")
	w(".agent-dev/credentials.json", "k")
	w("tokened/.npmrc", "//npm.pkg.github.com/:_authToken=abc")
	w("plain/.npmrc", "registry=https://registry.npmjs.org/")
	k := &knowledge.Knowledge{Status: knowledge.StatusVerified, AdapterFiles: []string{
		".agent-dev", "tokened", "plain", "missing.yaml", // paths outside the repo are refused by Load now
	}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	before := git(t, repo, "status", "--porcelain")

	res, err := AddWorktree(repo, "wt", "main")
	if err != nil {
		t.Fatal(err)
	}
	has := func(rel string) bool { _, err := os.Lstat(filepath.Join(res.Path, rel)); return err == nil }
	for _, ok := range []string{".agent-dev/app.compose.yaml", ".agent-dev/runtime.env", "plain/.npmrc"} {
		if !has(ok) {
			t.Errorf("%s should be copied", ok)
		}
	}
	for _, bad := range []string{".agent-dev/.env", ".agent-dev/.env.local", ".agent-dev/tls.key", ".agent-dev/tls.pem",
		".agent-dev/id_ed25519", ".agent-dev/credentials.json", "tokened/.npmrc"} {
		if has(bad) {
			t.Errorf("%s must never be copied", bad)
		}
	}
	if len(res.Copied) != 3 {
		t.Fatalf("copied %v skipped %v", res.Copied, res.Skipped)
	}
	if !strings.Contains(strings.Join(res.Skipped, "|"), "missing.yaml (missing)") {
		t.Fatalf("skipped %v", res.Skipped)
	}
	if git(t, repo, "status", "--porcelain") != before {
		t.Fatal("main checkout changed")
	}
}

func TestFetchUpdatesRemoteRefsOfThatRepoOnly(t *testing.T) {
	repo, origin := mkWithOrigin(t)
	clone := filepath.Join(tempRoot(t), "clone")
	git(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	git(t, clone, "checkout", "-q", "-b", "pushed")
	git(t, clone, "push", "-q", "origin", "pushed")
	if _, err := Fetch(repo); err != nil {
		t.Fatal(err)
	}
	if !refExists(repo, "refs/remotes/origin/pushed") {
		t.Fatal("fetch did not bring the new remote branch")
	}
}

// A branch already checked out somewhere (here: main, in the main checkout)
// gets no second worktree: the error names that checkout and the start-env
// command for it, so an agent does not have to guess.
func TestAddWorktreeBranchAlreadyCheckedOut(t *testing.T) {
	repo, _ := mkWithOrigin(t)
	_, err := AddWorktree(repo, "main", "")
	if err == nil {
		t.Fatal("main is checked out in the main checkout; expected a refusal")
	}
	for _, want := range []string{"already checked out at " + repo, "stackfork start-env " + shellArg(repo)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %q", want, err)
		}
	}
	if _, serr := os.Stat(filepath.Join(repo, ".worktrees", "main")); serr == nil {
		t.Error("no folder may be created")
	}
}

// The learn prompt sends an agent to the checkout that already has the branch
// instead of asking it to create a worktree that git would refuse.
func TestPromptUsesTheCheckoutThatHasTheBranch(t *testing.T) {
	repo, _ := mkWithOrigin(t)
	p := Prompt("proj", repo, "main")
	if strings.Contains(p, "worktree add") || !strings.Contains(p, "already checked out at `"+repo+"`") {
		t.Fatalf("main is checked out in the main checkout:\n%s", p)
	}
	git(t, repo, "branch", "feature")
	if p := Prompt("proj", repo, "feature"); !strings.Contains(p, "stackfork worktree add") {
		t.Fatalf("a branch checked out nowhere needs a worktree:\n%s", p)
	}
}

// entry-links task 6.1: the copied prompt asks for the entry and the links.
func TestPromptNamesEntryAndLinks(t *testing.T) {
	p := Prompt("acme-web", t.TempDir(), "main")
	for _, want := range []string{"the local URLs that matter", "stackfork learn record entry", "learn record link", "Storybook"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
}

// The copied prompt tells the agent how to ask for a credential.
func TestPromptNamesSecrets(t *testing.T) {
	p := Prompt("acme-web", t.TempDir(), "main")
	for _, want := range []string{"stackfork learn record secret NAME", "never ask me to paste a value"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
}

func TestPromptEntrypointStartup(t *testing.T) {
	p := Prompt("proj", t.TempDir(), "main")
	if !strings.Contains(p, "already migrates or seeds on start") || !strings.Contains(p, "must not repeat the migrate step") {
		t.Fatalf("prompt lacks the startup guidance: %s", p)
	}
}
