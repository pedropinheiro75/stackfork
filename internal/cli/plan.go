package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// cmdPlan prints the startup plan for a mode, or (--trust) records this
// machine's trust in its host and prepare steps. --trust never prompts: the
// dashboard runs it after its own native confirmation.
func cmdPlan(stdout, stderr io.Writer, subArgs []string, root, stateBase string) (int, error) {
	fs := newFlagSet(stdout, "plan")
	modeFlag := fs.String("mode", "", "")
	jsonFlag := fs.Bool("json", false, "")
	trustFlag := fs.Bool("trust", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	main := repos.MainRepoOf(root)
	if main == "" {
		main = root
	}
	// same lookup as start-env: the checkout's own file, then the main repo's
	k, err := knowledge.LoadFirst(root, main)
	if err == nil && k == nil {
		err = errors.New("knowledge.json not found")
	}
	if err != nil {
		return 2, fmt.Errorf("no startup plan; run `stackfork learn` first: %w", err)
	}

	if *trustFlag {
		if len(k.Plan.HostSteps()) == 0 {
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
	if s.Kind != knowledge.KindHost && s.Kind != knowledge.KindPrepare {
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
