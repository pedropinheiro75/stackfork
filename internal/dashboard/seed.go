package dashboard

import (
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// recipeView is a repo's read-only startup-recipe summary for the Repos tab.
// State is "seed" (a recorded command), "never" (recorded `none`), "auto"
// (nothing recorded: a new database is seeded with the detected script) or
// "none" (nothing recorded, nothing detected).
type recipeView struct {
	Migrate  string
	Detected string
	Seed     string
	State    string
}

func (s *Server) repoRecipe(repo repos.Repo) recipeView {
	migrate, detected := learn.DetectSeed(repo.Path)
	v := recipeView{Migrate: migrate, Detected: detected, State: "none"}
	if detected != "" {
		v.State = "auto"
	}
	if k, err := knowledge.Load(filepath.Join(repo.Path, ".stackfork", "knowledge.json")); err == nil {
		if m := k.Plan.MigrateStep(); m != nil {
			v.Migrate = m.Cmd
		}
	}
	decided, cmd, _ := learn.SeedDecision([]string{repo.Path}, nil)
	for _, wt := range repo.Worktrees {
		if decided || wt.Instance == nil {
			continue
		}
		if _, _, _, info, err := s.resolveInstance(wt.Instance.Project); err == nil {
			decided, cmd, _ = learn.SeedDecision(nil, info)
		}
	}
	switch {
	case decided && learn.SeedActive(cmd):
		v.State, v.Seed = "seed", cmd
	case decided:
		v.State = "never"
	}
	return v
}
