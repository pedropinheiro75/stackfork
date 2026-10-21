package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// resolvePrepare names where the prepare arguments come from; each source has
// a label, and none of them writes a file.
func TestResolvePrepareSources(t *testing.T) {
	adapter := func(t *testing.T, root, name string) {
		t.Helper()
		os.MkdirAll(filepath.Join(root, ".stackfork"), 0o755)
		os.WriteFile(filepath.Join(root, ".stackfork", name), []byte("services: {}\n"), 0o644)
	}
	know := func(t *testing.T, root string, k knowledge.Knowledge) {
		t.Helper()
		k.Schema = knowledge.CurrentSchema
		if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, root, inst string)
		from  string
		args  []string
		reuse bool
	}{
		{"manifest", func(t *testing.T, root, inst string) {
			os.MkdirAll(inst, 0o755)
			if err := state.WritePrivate(filepath.Join(inst, "manifest.json"), &state.Manifest{Files: []string{filepath.Join(root, "compose.yaml")}}); err != nil {
				t.Fatal(err)
			}
		}, prepareFromManifest, nil, true},
		{"plan", func(t *testing.T, root, inst string) {
			know(t, root, knowledge.Knowledge{Plan: knowledge.Plan{Steps: []knowledge.Step{{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", "compose.yaml"}}}}})
		}, prepareFromPlan, []string{"-f", "compose.yaml"}, false},
		{"adapter_files", func(t *testing.T, root, inst string) {
			adapter(t, root, "dev.compose.yaml")
			know(t, root, knowledge.Knowledge{AdapterFiles: []string{".stackfork/dev.compose.yaml"}})
		}, prepareFromAdapterFiles, []string{"-f", ".stackfork/dev.compose.yaml"}, false},
		{"adapter", func(t *testing.T, root, inst string) { adapter(t, root, "a.compose.yaml") },
			prepareFromAdapter, []string{"-f", ".stackfork/a.compose.yaml"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := scratchRepo(t, filepath.Join(t.TempDir(), "acme-web"))
			inst := filepath.Join(t.TempDir(), "inst")
			c.setup(t, root, inst)
			ch, err := resolvePrepare(root, root, inst, nil)
			if err != nil {
				t.Fatal(err)
			}
			if ch.From != c.from || ch.Reuse != c.reuse || !reflect.DeepEqual(ch.Args, c.args) || ch.Sibling != nil {
				t.Errorf("%+v", ch)
			}
		})
	}
	t.Run("no recipe", func(t *testing.T) {
		root := scratchRepo(t, filepath.Join(t.TempDir(), "acme-web"))
		if _, err := resolvePrepare(root, root, filepath.Join(t.TempDir(), "i"), nil); !errors.Is(err, errNoRecipe) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("adapter only in main", func(t *testing.T) {
		repo := scratchRepo(t, filepath.Join(t.TempDir(), "acme-web"))
		adapter(t, repo, "dev.compose.yaml")
		nw := filepath.Join(repo, ".worktrees", "feat")
		if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat", nw).CombinedOutput(); err != nil {
			t.Fatalf("%s", out)
		}
		_, err := resolvePrepare(nw, repo, filepath.Join(t.TempDir(), "i"), nil)
		if err == nil || !strings.Contains(err.Error(), "exists only in the main checkout") {
			t.Fatalf("%v", err)
		}
	})
}

func TestResolvePrepareSiblingWritesNothing(t *testing.T) {
	repo, sib, base := siblingRepo(t)
	nw := filepath.Join(repo, ".worktrees", "feat-b")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat-b", nw).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	before := listTree(t, nw)
	ch, err := resolvePrepare(nw, repo, filepath.Join(t.TempDir(), "i"), collectInstances(base[1], base[3]))
	if err != nil {
		t.Fatal(err)
	}
	if ch.From != prepareFromSibling || ch.Sibling == nil || ch.Sibling.Source != sib || ch.Reuse || len(ch.Args) == 0 {
		t.Errorf("%+v", ch)
	}
	if after := listTree(t, nw); !reflect.DeepEqual(before, after) {
		t.Errorf("resolvePrepare wrote files:\nbefore %v\nafter  %v", before, after)
	}
}

// listTree lists every path under dir except .git.
func listTree(t *testing.T, dir string) []string {
	t.Helper()
	var l []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !(info.IsDir() && info.Name() == ".git") && !strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			l = append(l, p)
		}
		return nil
	})
	return l
}
