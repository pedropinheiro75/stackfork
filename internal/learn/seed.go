package learn

import (
	"encoding/json"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SeedNone is the recorded answer "never seed this project".
const SeedNone = "none"

// SeedActive reports whether a recorded seed_once is a command to run.
func SeedActive(cmd string) bool { return cmd != "" && cmd != SeedNone }

// DetectSeed finds the project's migrate and seed commands from its scripts
// (package.json, Makefile, justfile) and the usual framework conventions
// (prisma, rails, django). Either may be "". Nothing is run or read beyond
// file names and script text.
func DetectSeed(root string) (migrate, seed string) {
	mgr, _, _ := detectPackageManager(root)
	if mgr == "" {
		mgr = "npm"
	}
	for _, s := range detectScripts(root) {
		var cmd string
		switch s.Source {
		case "package.json":
			cmd = mgr + " run " + s.Name
		case "Makefile":
			cmd = "make " + s.Name
		case "justfile":
			cmd = "just " + s.Name
		}
		if s.Role == "migrate" && migrate == "" {
			migrate = cmd
		}
		if s.Role == "seed" && seed == "" {
			seed = cmd
		}
	}
	if seed == "" {
		if data, ok := readFileNoSymlink(filepath.Join(root, "package.json")); ok {
			var pkg struct {
				Prisma struct {
					Seed string `json:"seed"`
				} `json:"prisma"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Prisma.Seed != "" {
				seed = map[bool]string{true: "bunx", false: "npx"}[mgr == "bun"] + " prisma db seed"
			}
		}
	}
	if seed == "" && fileExists(filepath.Join(root, "db", "seeds.rb")) && fileExists(filepath.Join(root, "Gemfile")) {
		seed = "bin/rails db:seed"
		if migrate == "" {
			migrate = "bin/rails db:migrate"
		}
	}
	if seed == "" && fileExists(filepath.Join(root, "manage.py")) {
		if m, _ := filepath.Glob(filepath.Join(root, "*", "fixtures", "*.json")); len(m) > 0 {
			rel, _ := filepath.Rel(root, m[0])
			seed = "python manage.py loaddata " + filepath.ToSlash(rel)
			if migrate == "" {
				migrate = "python manage.py migrate"
			}
		}
	}
	return migrate, seed
}

// SeedDecision looks for a recorded decision about seeding: a decided
// knowledge recipe in any of dirs (first wins), else the manifest's. cmd is
// SeedNone for "never".
func SeedDecision(dirs []string, m *state.Manifest) (decided bool, cmd, service string) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if k, err := knowledge.Load(filepath.Join(d, ".stackfork", "knowledge.json")); err == nil && k.Recipe.SeedOnce != "" && !k.Recipe.SeedPending {
			return true, k.Recipe.SeedOnce, k.Recipe.SeedService
		}
	}
	if m != nil && m.SeedOnce != "" {
		return true, m.SeedOnce, m.SeedService
	}
	return false, "", ""
}
