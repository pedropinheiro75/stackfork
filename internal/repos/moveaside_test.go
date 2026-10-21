package repos

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
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
	if fi, _ := os.Stat(filepath.Join(dest, "old.compose.yaml")); fi.Mode().Perm() != 0o640 {
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
