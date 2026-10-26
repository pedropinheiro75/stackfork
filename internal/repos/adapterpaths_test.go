package repos

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A borrowed adapter compose file with the source checkout's absolute paths
// must point at the new checkout, or prepare refuses its env_file.
func TestApplyRewritesSourcePathsInAdapterCompose(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	compose := "services:\n  app:\n    env_file:\n      - " + src + "/.agent-dev/learn.env\n    build:\n      context: \"" + src + "\"\n    labels: [" + src + "-2/x]\n"
	write(t, filepath.Join(src, ".agent-dev", "learn-compose.yml"), compose, 0o644)
	r := &SiblingRecipe{Source: src, untracked: []string{".agent-dev/learn-compose.yml"}}
	if _, err := r.Apply(dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, ".agent-dev", "learn-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{dst + "/.agent-dev/learn.env", `context: "` + dst + `"`, src + "-2/x"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ReplaceAll(got, src+"-2", ""), src) {
		t.Fatalf("source path left:\n%s", got)
	}
}

// A file an older stackfork already copied with a sibling's paths is fixed in
// place; a path under root (root inside main/.worktrees) is left alone.
func TestRepointAdapterComposeFixesAnOldCopy(t *testing.T) {
	main, wt, _ := mainRecipeFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	f := filepath.Join(wt, ".agent-dev", "c.yml")
	write(t, f, "env_file: ["+main+"/.agent-dev/x.env]\nctx: "+wt+"/a\n", 0o644)
	fixed, err := RepointAdapterCompose(main, wt, []string{"-f", ".agent-dev/c.yml", "-f", other})
	if err != nil || len(fixed) != 1 {
		t.Fatalf("%v %v", fixed, err)
	}
	b, _ := os.ReadFile(f)
	want := "env_file: [" + wt + "/.agent-dev/x.env]\nctx: " + wt + "/a\n"
	if string(b) != want {
		t.Fatalf("got\n%s\nwant\n%s", b, want)
	}
}
