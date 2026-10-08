package learn

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func put(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectSeed(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		migrate string
		seed    string
	}{
		{"bun scripts", map[string]string{"package.json": `{"scripts":{"db:migrate":"x","db:seed":"y","db:reset":"z"}}`, "bun.lock": ""}, "bun run db:migrate", "bun run db:seed"},
		{"npm default", map[string]string{"package.json": `{"scripts":{"seed":"y"}}`}, "", "npm run seed"},
		{"makefile", map[string]string{"Makefile": "seed:\n\t./seed.sh\n"}, "", "make seed"},
		{"justfile", map[string]string{"justfile": "db-seed:\n    ./seed.sh\n"}, "", "just db-seed"},
		{"prisma", map[string]string{"package.json": `{"prisma":{"seed":"ts-node seed.ts"}}`}, "", "npx prisma db seed"},
		{"rails", map[string]string{"Gemfile": "", "db/seeds.rb": ""}, "bin/rails db:migrate", "bin/rails db:seed"},
		{"django", map[string]string{"manage.py": "", "app/fixtures/dev.json": "[]"}, "python manage.py migrate", "python manage.py loaddata app/fixtures/dev.json"},
		{"nothing", map[string]string{"package.json": `{"scripts":{"dev":"x"}}`}, "", ""},
	}
	for _, c := range cases {
		root := t.TempDir()
		for n, b := range c.files {
			put(t, root, n, b)
		}
		if m, s := DetectSeed(root); m != c.migrate || s != c.seed {
			t.Errorf("%s: got (%q, %q) want (%q, %q)", c.name, m, s, c.migrate, c.seed)
		}
	}
}

func TestSeedDecisionPrecedence(t *testing.T) {
	root := t.TempDir()
	if d, _, _ := SeedDecision([]string{root}, nil); d {
		t.Fatal("nothing recorded is undecided")
	}
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Recipe: knowledge.Recipe{SeedOnce: "make seed", SeedPending: true}}
	if err := WriteKnowledge(root, k); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := SeedDecision([]string{root}, nil); d {
		t.Fatal("a pending proposal is not a decision")
	}
	if d, c, _ := SeedDecision([]string{root}, &state.Manifest{SeedOnce: SeedNone}); !d || SeedActive(c) {
		t.Fatalf("manifest 'none' = decided, never: %v %q", d, c)
	}
	k.Recipe.SeedPending = false
	if err := WriteKnowledge(root, k); err != nil {
		t.Fatal(err)
	}
	if d, c, _ := SeedDecision([]string{root}, &state.Manifest{SeedOnce: SeedNone}); !d || c != "make seed" {
		t.Fatalf("knowledge wins: %v %q", d, c)
	}
}
