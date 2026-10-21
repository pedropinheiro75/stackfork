package dashboard

import (
	"strconv"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// entryView is the Repos entry line (entry-links D12): State is recorded,
// guessed or none; Target is "service:port[path]", empty when unknown.
// Service and Port are the known target of a guess, for the copy command.
type entryView struct {
	State, Target, Service string
	Port                   int
}

// entryCommand is the copy-only command of a guessed entry: the guessed
// service and port when known, else the placeholders S and N. The page never
// runs it.
func entryCommand(v *entryView) string {
	service, port := "S", "N"
	if v != nil && v.Service != "" {
		service, port = v.Service, strconv.Itoa(v.Port)
	}
	return "stackfork learn record entry --service " + service + " --port " + port
}

// repoEntry reads the main checkout's knowledge; nil when there is none.
// With no entry recorded it shows the scan's proposal, when there is one.
func repoEntry(repo repos.Repo) *entryView {
	k, err := knowledge.LoadFirst(repo.Path)
	if err != nil || k == nil {
		return nil
	}
	e := k.Entry
	if e == nil {
		e = learn.ProposeEntry(k)
	}
	v := &entryView{}
	if e != nil {
		v.Service, v.Port = e.Service, e.Port
	}
	if k.Entry == nil && e != nil {
		k = &knowledge.Knowledge{Entry: e}
	}
	v.State, v.Target = k.EntryLine()
	return v
}

// stepView is one row of a repo's plan list.
type stepView struct {
	ID, Kind, Text, Port string
	Source               string // a derived step: where it comes from
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
	// Derived: nobody recorded these steps; they are what a Start would run
	// (add-derived-plan-view). Refusal replaces the list when it would not.
	Derived bool
	Refusal string
	Gap     string
}

// repoPlan returns the plan list of the main checkout: the recorded one, or,
// for a repo whose knowledge has no plan steps, the derived one. nil when
// there is nothing to show.
func (s *Server) repoPlan(repo repos.Repo, cfg *config.Config) *planView {
	k, err := knowledge.LoadFirst(repo.Path)
	if err != nil { // shown as a failed callout, not hidden
		return &planView{LoadError: err.Error()}
	}
	if k == nil || len(k.Plan.Steps) == 0 {
		return s.derivedPlan(repo, cfg)
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
		v.Steps = append([]stepView{secretsRow("")}, v.Steps...)
	}
	return v
}

// derivedPlan is the plan list of a repo with no recorded steps. The hook's
// result is cached per repo until a scan or an action drops it.
func (s *Server) derivedPlan(repo repos.Repo, cfg *config.Config) *planView {
	if s.DerivePlan == nil {
		return nil
	}
	s.repos.mu.Lock()
	e, ok := s.repos.derived[repo.Path]
	insts := s.repos.insts
	s.repos.mu.Unlock()
	if !ok {
		e.plan, e.err = s.DerivePlan(repo.Path, insts)
		s.repos.mu.Lock()
		if s.repos.derived == nil {
			s.repos.derived = map[string]derivedEntry{}
		}
		s.repos.derived[repo.Path] = e
		s.repos.mu.Unlock()
	}
	if e.err != nil {
		return &planView{LoadError: e.err.Error()}
	}
	d := e.plan
	v := &planView{Derived: true, Mode: d.Mode, Modes: []string{d.Mode}, Status: lifecycle.StateDerived, Trusted: true,
		Refusal: d.Refusal, Gap: d.Gap}
	for _, st := range d.Steps {
		v.Steps = append(v.Steps, stepView{ID: st.ID, Kind: st.Kind, Text: lifecycle.StepText(st.Step), Source: st.Source,
			Chip: chipView{Class: "off", Glyph: glyphOff, Label: lifecycle.StateDerived, Title: st.Source}})
	}
	if len(v.Steps) > 0 && hasSecretNames(repo.Path, cfg) {
		v.Steps = append([]stepView{secretsRow("secrets preflight")}, v.Steps...)
	}
	return v
}

// secretsRow is the first row of a plan list: a Start resolves secrets before
// anything else (preflight), so a failure there needs a row to show in.
func secretsRow(source string) stepView {
	return stepView{ID: knowledge.KindSecrets, Kind: knowledge.KindSecrets, Text: lifecycle.PreflightText, Source: source,
		Chip: chipView{Class: "off", Glyph: glyphOff, Label: "at start", Title: "stackfork resolves the secrets before anything else runs"}}
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
