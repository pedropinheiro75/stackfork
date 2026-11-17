package repos

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func touch(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectEcosystems(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  []string
	}{
		{"none", []string{"README.md", "main.py"}, nil},
		{"go only", []string{"go.mod", "main.go"}, []string{"go"}},
		{"js only", []string{"package.json"}, []string{"js"}},
		{"mixed root", []string{"go.mod", "package.json"}, []string{"go", "js"}},
		{"mixed monorepo", []string{"apps/web/package.json", "services/api/go.mod"}, []string{"go", "js"}},
		{"dependency folders ignored", []string{"node_modules/x/package.json", "vendor/y/go.mod", ".hidden/package.json"}, nil},
	}
	for _, c := range cases {
		root := t.TempDir()
		touch(t, root, c.files...)
		if got := detectEcosystems(root); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSetupGuidanceByEcosystem(t *testing.T) {
	has := func(s, sub string) bool { return strings.Contains(s, sub) }
	goRoot, jsRoot, mixed, none := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	touch(t, goRoot, "go.mod")
	touch(t, jsRoot, "package.json")
	touch(t, mixed, "go.mod", "package.json")
	g, j, m, n := setupGuidance(goRoot), setupGuidance(jsRoot), setupGuidance(mixed), setupGuidance(none)
	if !has(g, "Go project") || has(g, "yarn install") {
		t.Errorf("go: %s", g)
	}
	if !has(j, "JavaScript or TypeScript project") || has(j, "Go project") {
		t.Errorf("js: %s", j)
	}
	if !has(m, "Go project") || !has(m, "JavaScript or TypeScript project") {
		t.Errorf("mixed: %s", m)
	}
	if has(n, "Go project") || has(n, "yarn install") || has(n, "JavaScript") {
		t.Errorf("none: %s", n)
	}
	for _, s := range []string{g, j, m, n} {
		for _, want := range []string{"Record a step only for a task the project really has", "already migrates or seeds on start", "stackfork learn verify", "Ask before building"} {
			if !has(s, want) {
				t.Errorf("base text lacks %q: %s", want, s)
			}
		}
	}
}
