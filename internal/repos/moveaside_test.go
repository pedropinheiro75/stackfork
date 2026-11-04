package repos

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

var fixedNow = time.Date(2026, 10, 20, 14, 22, 33, 0, time.UTC)

// mainRecipeFixture: a main checkout with a tracked compose.yaml and tracked
// .stackfork/tracked.yml, untracked .stackfork/dev.compose.yaml and
// runtime.env, prepared with both; a linked worktree with its own untracked
// adapter files; and a more recent instance of a third checkout.
func mainRecipeFixture(t *testing.T) (main, wt string, insts []inventory.Instance) {
	t.Helper()
	main = mkRepo(t, filepath.Join(tempRoot(t), "acme-web"))
	write(t, filepath.Join(main, "compose.yaml"), "services: {app: {image: x}}\n", 0o644)
	write(t, filepath.Join(main, ".stackfork", "tracked.yml"), "tracked: true\n", 0o644)
	git(t, main, "add", ".")
	git(t, main, "commit", "-q", "-m", "compose")
	write(t, filepath.Join(main, ".stackfork", "dev.compose.yaml"), "services: {app: {environment: {A: main}}}\n", 0o644)
	write(t, filepath.Join(main, ".stackfork", "runtime.env"), "DEV_CHECKOUT="+main+"\n", 0o600)
	res, err := AddWorktree(main, "feat-a", "main")
	if err != nil {
		t.Fatal(err)
	}
	wt = res.Path
	write(t, filepath.Join(wt, ".stackfork", "old.compose.yaml"), "old\n", 0o640)
	write(t, filepath.Join(wt, ".stackfork", "knowledge.json"), "{}\n", 0o644)
	write(t, filepath.Join(wt, ".stackfork", "runtime.env"), "OLD=1\n", 0o600)
	other, err := AddWorktree(main, "feat-b", "main")
	if err != nil {
		t.Fatal(err)
	}
	mainInst := inventory.Instance{Project: "sf-main", Root: main, Used: time.Now().Add(-time.Hour),
		Files:   []string{filepath.Join(main, "compose.yaml"), filepath.Join(main, ".stackfork", "dev.compose.yaml")},
		EnvFile: filepath.Join(main, ".stackfork", "runtime.env")}
	otherInst := inventory.Instance{Project: "sf-feat-b", Root: other.Path, Used: time.Now(),
		Files: []string{filepath.Join(other.Path, "compose.yaml")}}
	return main, wt, []inventory.Instance{otherInst, mainInst}
}

func TestFindMainRecipeIgnoresOtherWorktrees(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	r, err := FindMainRecipe(main, wt, insts)
	if err != nil || r == nil {
		t.Fatalf("%v %v", r, err)
	}
	if r.Project != "sf-main" || strings.Join(r.Files, ",") != "compose.yaml,.stackfork/dev.compose.yaml" || r.EnvFile != ".stackfork/runtime.env" {
		t.Fatalf("recipe %+v", r)
	}
	if r, err := FindMainRecipe(main, wt, insts[:1]); r != nil || err != nil {
		t.Fatalf("no main instance: %v %v", r, err)
	}
}

func TestMoveAsideMovesOnlyUntrackedAdapterFiles(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	r, _ := FindMainRecipe(main, wt, insts)
	old := now
	now = func() time.Time { return fixedNow }
	defer func() { now = old }()
	files, err := AsideFiles(wt, r)
	if err != nil || strings.Join(files, ",") != ".stackfork/knowledge.json,.stackfork/old.compose.yaml,.stackfork/runtime.env" {
		t.Fatalf("preview %v %v", files, err)
	}
	rep, err := MoveAside(wt, r)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Dir != ".stackfork-aside/20261020T142233Z" || strings.Join(rep.Moved, ",") != strings.Join(files, ",") {
		t.Fatalf("report %+v", rep)
	}
	dest := filepath.Join(wt, ".stackfork-aside", "20261020T142233Z", ".stackfork")
	for _, f := range []string{"knowledge.json", "old.compose.yaml", "runtime.env"} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("%s not under aside: %v", f, err)
		}
		if _, err := os.Lstat(filepath.Join(wt, ".stackfork", f)); err == nil {
			t.Errorf("%s still in place", f)
		}
	}
	if fi, _ := os.Stat(filepath.Join(dest, "old.compose.yaml")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(wt, ".stackfork-aside", "20261020T142233Z")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("aside folder mode %v", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(wt, ".stackfork", "tracked.yml")); err != nil {
		t.Errorf("a tracked file moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "compose.yaml")); err != nil {
		t.Errorf("compose.yaml moved: %v", err)
	}
}

func TestMoveAsideStopsOnAnExistingDestination(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	r, _ := FindMainRecipe(main, wt, insts)
	old := now
	now = func() time.Time { return fixedNow }
	defer func() { now = old }()
	write(t, filepath.Join(wt, ".stackfork-aside", "20261020T142233Z", ".stackfork", "old.compose.yaml"), "already\n", 0o600)
	rep, err := MoveAside(wt, r)
	if err == nil || !strings.Contains(err.Error(), "old.compose.yaml") {
		t.Fatalf("want an error naming the file: %v", err)
	}
	// sorted: knowledge.json moved before the clash
	if strings.Join(rep.Moved, ",") != ".stackfork/knowledge.json" {
		t.Fatalf("moved %v", rep.Moved)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, ".stackfork", "old.compose.yaml")); string(b) != "old\n" {
		t.Fatal("the clashing source was touched")
	}
}

func TestMoveAsideReportsWhatMovedWhenARenameFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root POSIX user to make a folder read-only")
	}
	wt := mkRepo(t, filepath.Join(tempRoot(t), "wt"))
	write(t, filepath.Join(wt, ".agent-dev", "a.yml"), "a\n", 0o644)
	write(t, filepath.Join(wt, ".stackfork", "b.yml"), "b\n", 0o644)
	if err := os.Chmod(filepath.Join(wt, ".stackfork"), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(wt, ".stackfork"), 0o755)
	r := &SiblingRecipe{untracked: []string{".agent-dev/x.yml", ".stackfork/y.yml"}}
	rep, err := MoveAside(wt, r)
	if err == nil {
		t.Fatal("want a rename error")
	}
	if strings.Join(rep.Moved, ",") != ".agent-dev/a.yml" {
		t.Fatalf("moved %v", rep.Moved)
	}
	if _, err := os.Stat(filepath.Join(wt, ".stackfork", "b.yml")); err != nil {
		t.Fatal("the unmoved file is gone")
	}
}

// C1: an untracked root file in the recipe must never turn "." into a
// folder to sweep (it would move the worktree's .git file).
func TestAsideFilesRefusesAnUntrackedRecipeFileOutsideTheAdapterFolders(t *testing.T) {
	_, wt, _ := mainRecipeFixture(t)
	write(t, filepath.Join(wt, "notes.md"), "n\n", 0o644)
	write(t, filepath.Join(wt, ".env.local"), "E=1\n", 0o600)
	r := &SiblingRecipe{untracked: []string{"compose.override.yaml"}}
	files, err := AsideFiles(wt, r)
	if err == nil || !strings.Contains(err.Error(), "compose.override.yaml") || files != nil {
		t.Fatalf("want a refusal naming the file, got %v %v", files, err)
	}
	if rep, err := MoveAside(wt, r); err == nil || len(rep.Moved) != 0 {
		t.Fatalf("MoveAside must refuse too: %+v %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(".git is gone")
	}
	if top := filepath.FromSlash(strings.TrimSpace(git(t, wt, "rev-parse", "--show-toplevel"))); top != wt {
		t.Fatalf("worktree detached: %s", top)
	}
	for _, f := range []string{"notes.md", ".env.local"} {
		if _, err := os.Stat(filepath.Join(wt, f)); err != nil {
			t.Fatalf("%s moved", f)
		}
	}
}

// C2: a symlinked adapter folder (pointing at main's) is refused, and
// nothing in main moves.
func TestAsideFilesRefusesASymlinkedAdapterFolder(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	os.RemoveAll(filepath.Join(wt, ".stackfork"))
	if err := os.Symlink(filepath.Join(main, ".stackfork"), filepath.Join(wt, ".stackfork")); err != nil {
		t.Skip("symlinks unavailable")
	}
	r, _ := FindMainRecipe(main, wt, insts)
	if files, err := AsideFiles(wt, r); err == nil || files != nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("want a refusal, got %v %v", files, err)
	}
	if rep, err := MoveAside(wt, r); err == nil || len(rep.Moved) != 0 {
		t.Fatalf("MoveAside must refuse: %+v %v", rep, err)
	}
	for _, f := range []string{"dev.compose.yaml", "runtime.env"} {
		if _, err := os.Stat(filepath.Join(main, ".stackfork", f)); err != nil {
			t.Fatalf("main lost %s", f)
		}
	}
}

func TestAsideFilesRefusesASymlinkedAsideFolder(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	r, _ := FindMainRecipe(main, wt, insts)
	out := t.TempDir()
	if err := os.Symlink(out, filepath.Join(wt, ".stackfork-aside")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := MoveAside(wt, r); err == nil || !strings.Contains(err.Error(), ".stackfork-aside") {
		t.Fatalf("want a refusal: %v", err)
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Fatal("files went through the symlink")
	}
}

// A git failure other than "not tracked" (exit 1) is an error, never "untracked".
func TestAsideFilesTreatsOtherGitFailuresAsErrors(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	r, _ := FindMainRecipe(main, wt, insts)
	real, _ := exec.LookPath("git")
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *\" ls-files \"*) exit 3;; esac\nexec \"" + filepath.ToSlash(real) + "\" \"$@\"\n"
	testsupport.FakeExe(t, filepath.Join(dir, "git"), script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if files, err := AsideFiles(wt, r); err == nil || files != nil {
		t.Fatalf("want an error, got %v %v", files, err)
	}
	if rep, err := MoveAside(wt, r); err == nil || len(rep.Moved) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".stackfork", "old.compose.yaml")); err != nil {
		t.Fatal("a file moved")
	}
}

// M2: the main recipe never copies main's knowledge.json; the worktree falls
// back to main's file.
func TestMainRecipeApplyLeavesKnowledgeInMain(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	write(t, filepath.Join(main, ".stackfork", "knowledge.json"), "{}\n", 0o644)
	r, err := FindMainRecipe(main, wt, insts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MoveAside(wt, r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".stackfork", "knowledge.json")); err == nil {
		t.Fatal("main's knowledge.json was copied")
	}
	if _, err := os.Stat(filepath.Join(wt, ".stackfork", "dev.compose.yaml")); err != nil {
		t.Fatal("the recipe files were not copied")
	}
}

// A tracked env file outside the adapter folders is fine: tracked files never move.
func TestAsideFilesIgnoresATrackedRootEnvFile(t *testing.T) {
	_, wt, _ := mainRecipeFixture(t)
	r := &SiblingRecipe{untracked: []string{".stackfork/dev.compose.yaml"}, EnvFile: "dev.env"}
	files, err := AsideFiles(wt, r)
	if err != nil {
		t.Fatalf("a tracked root env file must not refuse: %v", err)
	}
	for _, f := range files {
		if f == "dev.env" {
			t.Fatal("dev.env must not move")
		}
	}
}

func TestAsideFilesRefusesAnAsideThatIsAFile(t *testing.T) {
	main, wt, insts := mainRecipeFixture(t)
	r, _ := FindMainRecipe(main, wt, insts)
	write(t, filepath.Join(wt, ".stackfork-aside"), "x", 0o644)
	if _, err := AsideFiles(wt, r); err == nil || !strings.Contains(err.Error(), ".stackfork-aside") {
		t.Fatalf("want a refusal: %v", err)
	}
}
