package repos

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

func gapFixture(t *testing.T) (main, wt string) {
	t.Helper()
	main = mkRepo(t, filepath.Join(tempRoot(t), "acme-web"))
	res, err := AddWorktree(main, "feat-a", "main")
	if err != nil {
		t.Fatal(err)
	}
	return main, res.Path
}

func mainInst(main string, legacy bool, used time.Time, rel ...string) inventory.Instance {
	var files []string
	for _, r := range rel {
		files = append(files, filepath.Join(main, r))
	}
	return inventory.Instance{Project: "sf-main", Root: main, Files: files, Legacy: legacy, Used: used}
}

func TestRecipeGap(t *testing.T) {
	main, wt := gapFixture(t)
	both := mainInst(main, false, time.Now(), "compose.yaml", ".stackfork/dev.compose.yaml")
	w := func(rel ...string) []string {
		var f []string
		for _, r := range rel {
			f = append(f, filepath.Join(wt, r))
		}
		return f
	}
	cases := []struct {
		name    string
		files   []string
		insts   []inventory.Instance
		missing string
		has     bool
	}{
		{"missing file", w(".stackfork/dev.compose.yaml"), []inventory.Instance{both}, "compose.yaml", true},
		{"extra file only", w("compose.yaml", ".stackfork/dev.compose.yaml", ".stackfork/extra.compose.yaml"), []inventory.Instance{both}, "", false},
		{"identical lists", w("compose.yaml", ".stackfork/dev.compose.yaml"), []inventory.Instance{both}, "", false},
		{"file outside the checkout is left out", []string{"/elsewhere/compose.yaml", filepath.Join(wt, ".stackfork/dev.compose.yaml")}, []inventory.Instance{both}, "compose.yaml", true},
		{"no main instance", w(".stackfork/dev.compose.yaml"), nil, "", false},
		{"legacy main instance", w(".stackfork/dev.compose.yaml"), []inventory.Instance{mainInst(main, true, time.Now(), "compose.yaml", ".stackfork/dev.compose.yaml")}, "compose.yaml", true},
		{"most recent main instance wins", w("a.yaml"), []inventory.Instance{
			mainInst(main, false, time.Now().Add(-time.Hour), "old.yaml"), mainInst(main, false, time.Now(), "a.yaml")}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := RecipeGap(main, wt, c.files, c.insts)
			if g.Has() != c.has || strings.Join(g.Missing, ",") != c.missing {
				t.Fatalf("gap %+v", g)
			}
			if !c.has && g.Message(wt) != "" {
				t.Fatalf("message without a gap: %q", g.Message(wt))
			}
		})
	}
	if g := RecipeGap(main, main, []string{filepath.Join(main, "x.yaml")}, []inventory.Instance{both}); g.Has() {
		t.Fatalf("the main checkout has no gap: %+v", g)
	}
}

func TestRecipeGapMessage(t *testing.T) {
	main, wt := gapFixture(t)
	g := RecipeGap(main, wt, []string{filepath.Join(wt, ".stackfork/dev.compose.yaml")},
		[]inventory.Instance{mainInst(main, false, time.Now(), "compose.yaml", ".stackfork/dev.compose.yaml")})
	want := "warning: this worktree prepares with 1 Compose file (.stackfork/dev.compose.yaml); the main checkout's last prepare used 2 (compose.yaml, .stackfork/dev.compose.yaml). Missing: compose.yaml. To reuse the main checkout's recipe: stackfork --root " + wt + " reset --from-main"
	if got := g.Message(wt); got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}
