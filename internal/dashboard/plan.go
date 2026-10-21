package dashboard

import (
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
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

// stepView is one row of a repo's plan list.
type stepView struct {
	ID, Kind, Text, Port string
	Skipped              bool
	Chip                 chipView
}

// planView is a repo's startup plan for its current mode, built by
// lifecycle.PlanView (the same code as `stackfork plan --json`).
type planView struct {
	LoadError           string // knowledge.json exists but cannot be loaded
	Unsupported         bool   // the configured mode is not one this plan supports
	TrustTitle          string // callout title
	Mode                string
	Modes               []string
	Status              string
	Trusted, NeedsTrust bool
	Diff                []string
	Steps, Skipped      []stepView
}

// repoPlan returns nil for a repo with no recorded steps; the page then
// falls back to the read-only recipe line.
func (s *Server) repoPlan(repo repos.Repo, cfg *config.Config) *planView {
	k, err := knowledge.LoadFirst(repo.Path)
	if err != nil { // shown as a failed callout, not hidden
		return &planView{LoadError: err.Error()}
	}
	if k == nil || len(k.Plan.Steps) == 0 {
		return nil
	}
	mode := knowledge.ModeContainer
	if cfg != nil {
		mode, _ = cfg.ModeForPlan(repo.Path, k.Plan)
	}
	v := &planView{Modes: []string{}}
	for _, m := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
		if k.Plan.SupportsMode(m) {
			v.Modes = append(v.Modes, m)
		}
	}
	v.Unsupported = !k.Plan.SupportsMode(mode)
	out := lifecycle.PlanView(s.NewStateBase, repo.Path, k, mode)
	v.Mode, v.Status, v.Trusted, v.NeedsTrust, v.Diff = out.Mode, out.Status, out.Trusted, out.TrustNeeded, out.Diff
	conv := func(in []lifecycle.PlanStep, skipped bool) (l []stepView) {
		for _, st := range in {
			l = append(l, stepView{ID: st.ID, Kind: st.Kind, Text: lifecycle.StepText(st.Step), Skipped: skipped, Chip: stepChip(st, skipped)})
		}
		return
	}
	v.TrustTitle = "Host steps are not trusted on this machine yet"
	if _, _, ok := knowledge.ReadTrust(knowledge.TrustPath(s.NewStateBase, repo.Path)); ok {
		v.TrustTitle = "Host steps changed since you trusted them"
	}
	v.Steps, v.Skipped = conv(out.Steps, false), conv(out.Skipped, true)
	// A Start resolves secrets before anything else (preflight, group 4): the
	// live list needs a row for it so a failure there has somewhere to show.
	if hasSecretNames(repo.Path, cfg) {
		v.Steps = append([]stepView{{ID: knowledge.KindSecrets, Kind: knowledge.KindSecrets, Text: lifecycle.PreflightText,
			Chip: chipView{Class: "off", Glyph: glyphOff, Label: "at start", Title: "stackfork resolves the secrets before anything else runs"}}}, v.Steps...)
	}
	return v
}

func stepChip(st lifecycle.PlanStep, skipped bool) chipView {
	switch {
	case skipped:
		return chipView{Class: "off", Glyph: glyphOff, Label: "skipped"}
	case st.Trust == "untrusted":
		return chipView{Class: "attention", Glyph: glyphBusy, Label: "untrusted"}
	case st.Trust == "trusted-here":
		return chipView{Class: "ok", Glyph: glyphOK, Label: "trusted"}
	case st.State == knowledge.StatusVerified:
		return chipView{Class: "ok", Glyph: glyphOK, Label: "verified"}
	}
	label := st.State
	if label == "" {
		label = "unverified"
	}
	return chipView{Class: "attention", Glyph: glyphBusy, Label: label}
}
