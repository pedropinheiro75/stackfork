package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// cmdPlan prints the startup plan for a mode, or (--trust) records this
// machine's trust in its host and prepare steps. --trust never prompts: the
// dashboard runs it after its own native confirmation.
func cmdPlan(stdout, stderr io.Writer, subArgs []string, root, stateBase, legacyStateBase string, profileFlag []string) (int, error) {
	fs := newFlagSet(stdout, "plan")
	modeFlag := fs.String("mode", "", "")
	jsonFlag := fs.Bool("json", false, "")
	trustFlag := fs.Bool("trust", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	if top, err := gitToplevel(root); err == nil { // a subdirectory finds its checkout, as start-env does
		if r, err := identity.ResolvePath(top); err == nil {
			root = r
		}
	}
	main := repos.MainRepoOf(root)
	if main == "" {
		main = root
	}
	// same lookup as start-env: the checkout's own file, then the main repo's
	k, from, err := knowledge.LoadFor(root, main)
	if err != nil { // a broken file is not an empty plan
		return 2, fmt.Errorf("no startup plan; run `stackfork learn` first: %w", err)
	}
	if k != nil && from != root && from != main && !*jsonFlag {
		fmt.Fprintf(stderr, "note: knowledge from worktree %s (verified)\n", filepath.Base(from))
	}
	if k == nil || len(k.Plan.Steps) == 0 { // nothing recorded: show what a Start would run
		return planDerived(stdout, stderr, root, stateBase, legacyStateBase, profileFlag, *modeFlag, *jsonFlag, *trustFlag)
	}

	if *trustFlag {
		if len(k.Plan.HostRunSteps()) == 0 {
			fmt.Fprintln(stderr, "stackfork: nothing to trust: this plan runs only in containers")
			return 2, nil
		}
		covered, err := lifecycle.TrustHere(stateBase, main, k.Plan)
		if err != nil {
			return 1, err
		}
		for _, s := range covered {
			fmt.Fprintln(stdout, "trusting "+strings.Join(lifecycle.StepBlock(s), "\n"))
		}
		fmt.Fprintln(stdout, "trusted on this machine")
		return 0, nil
	}

	mode := *modeFlag
	if mode == "" {
		if cfg, err := config.Load(); err == nil {
			var note string
			if mode, note = cfg.ModeForPlan(main, k.Plan); note != "" && !*jsonFlag {
				fmt.Fprintln(stderr, "note: "+note)
			}
		} else {
			mode = knowledge.ModeContainer
		}
	}
	if mode != knowledge.ModeContainer && mode != knowledge.ModeHybrid {
		return 1, fmt.Errorf("unknown mode %q (%s or %s)", mode, knowledge.ModeContainer, knowledge.ModeHybrid)
	}
	res := lifecycle.PlanView(stateBase, main, k, mode)
	if *jsonFlag {
		return 0, printJSON(stdout, res)
	}
	fmt.Fprintf(stdout, "plan (%s, mode %s)\n", orDash(res.Status), mode)
	for _, s := range res.Steps {
		fmt.Fprintln(stdout, "  "+planLine(s))
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintln(stdout, "skipped in this mode:")
		for _, s := range res.Skipped {
			fmt.Fprintln(stdout, "  "+planLine(s))
		}
	}
	return 0, nil
}

// planDerived prints the plan a Start would run for a checkout with no
// recorded plan steps (add-derived-plan-view D5). It exits 0 whether the
// start would run or be refused.
func planDerived(stdout, stderr io.Writer, root, stateBase, legacyStateBase string, profileFlag []string, mode string, asJSON, trust bool) (int, error) {
	if trust {
		fmt.Fprintln(stderr, "stackfork: nothing to trust: this plan runs only in containers")
		return 2, nil
	}
	if mode != "" && mode != knowledge.ModeContainer && mode != knowledge.ModeHybrid {
		return 1, fmt.Errorf("unknown mode %q (%s or %s)", mode, knowledge.ModeContainer, knowledge.ModeHybrid)
	}
	profiles, err := resolveProfileDirs(profileFlag)
	if err != nil {
		return 1, err
	}
	var note io.Writer = stderr
	if asJSON {
		note = io.Discard
	}
	d, err := DerivePlan(DeriveOptions{Checkout: root, NewStateBase: stateBase, LegacyStateBase: legacyStateBase, ProfileDirs: profiles, Mode: mode, Out: note})
	if err != nil {
		return 1, err
	}
	if asJSON {
		return 0, printJSON(stdout, d.View())
	}
	fmt.Fprintf(stdout, "plan (derived — not recorded; run learn to record it, mode %s)\n", d.Mode)
	if d.Refusal != "" {
		fmt.Fprintln(stdout, "start-env would refuse this checkout: "+d.Refusal)
		return 0, nil
	}
	for _, s := range d.Steps {
		fmt.Fprintln(stdout, "  "+derivedLine(s))
	}
	if d.Gap != "" {
		fmt.Fprintln(stdout, d.Gap)
	}
	return 0, nil
}

// derivedLine is a derived step: "id [kind] command - source".
func derivedLine(s lifecycle.PlanStep) string {
	l := s.ID + " [" + s.Kind + "]"
	if s.Cmd != "" {
		l += " " + s.Cmd
	}
	if s.Service != "" {
		l += " (service " + s.Service + ")"
	}
	if s.Step.IsHostExec() {
		l += " (on host)"
	}
	return l + " - " + s.Source
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func planLine(s lifecycle.PlanStep) string {
	suffix := " - " + s.State
	if s.Trust != "" {
		suffix += ", " + s.Trust
	}
	if s.Kind != knowledge.KindHost && s.Kind != knowledge.KindPrepare && !s.Step.IsHostExec() {
		l := s.ID + " [" + s.Kind + "]"
		if s.Cmd != "" {
			l += " " + s.Cmd
		}
		if s.Service != "" {
			l += " (service " + s.Service + ")"
		}
		if len(s.Exclude) > 0 {
			l += " exclude " + strings.Join(s.Exclude, ",")
		}
		return l + suffix
	}
	b := lifecycle.StepBlock(s.Step)
	if s.Kind == knowledge.KindHost && s.Port == 0 {
		b = append(b, "    port: unknown")
	}
	b[0] += suffix
	return strings.Join(b, "\n  ")
}
