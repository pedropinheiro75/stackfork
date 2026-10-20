package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

type planStepOut struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Cmd     string   `json:"cmd,omitempty"`
	Service string   `json:"service,omitempty"`
	Args    []string `json:"args,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	Name    string   `json:"name,omitempty"`
	Port    int      `json:"port,omitempty"`
	Once    string   `json:"once,omitempty"`
	Modes   []string `json:"modes,omitempty"`
	State   string   `json:"state"`
	Trust   string   `json:"trust"`
}

type planOut struct {
	Mode     string        `json:"mode"`
	Status   string        `json:"status"`
	Verified bool          `json:"verified"`
	Trusted  bool          `json:"trusted"`
	Steps    []planStepOut `json:"steps"`
	Skipped  []planStepOut `json:"skipped"`
}

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
	k, err := knowledge.Load(knowledgePath(root))
	if err != nil {
		return 2, fmt.Errorf("no startup plan; run `stackfork learn` first: %w", err)
	}
	main := repos.MainRepoOf(root)
	if main == "" {
		main = root
	}

	if *trustFlag {
		if len(k.Plan.HostSteps()) == 0 {
			fmt.Fprintln(stderr, "stackfork: nothing to trust: this plan runs only in containers")
			return 2, nil
		}
		covered := knowledge.TrustSteps(k.Plan.Steps)
		for _, s := range covered {
			fmt.Fprintf(stdout, "trusting %s\n", describeStep(s))
		}
		if err := knowledge.WriteTrust(knowledge.TrustPath(stateBase, main), knowledge.PlanHash(k.Plan.Steps), covered); err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, "trusted on this machine")
		return 0, nil
	}

	mode := *modeFlag
	if mode == "" {
		if cfg, err := config.Load(); err == nil {
			mode = cfg.ModeFor(main)
		} else {
			mode = knowledge.ModeContainer
		}
	}
	trusted, _ := lifecycle.TrustedHere(stateBase, main, k.Plan)
	run, skipped := k.Plan.ForMode(mode)
	state := k.Plan.Status
	if k.Status == knowledge.StatusVerified {
		state = knowledge.StatusVerified
	}
	conv := func(in []knowledge.Step) []planStepOut {
		out := []planStepOut{}
		for _, s := range in {
			o := planStepOut{ID: s.ID, Kind: s.Kind, Cmd: s.Cmd, Service: s.Service, Args: s.Args, Exclude: s.Exclude,
				Name: s.Name, Port: s.Port, Once: s.Once, Modes: s.Modes, State: state}
			if s.Kind == knowledge.KindHost || s.Kind == knowledge.KindPrepare {
				o.Trust = "untrusted"
				if trusted {
					o.Trust = "trusted-here"
				}
			}
			out = append(out, o)
		}
		return out
	}
	res := planOut{Mode: mode, Status: k.Plan.Status, Verified: k.Status == knowledge.StatusVerified,
		Trusted: trusted, Steps: conv(run), Skipped: conv(skipped)}
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

func describeStep(s knowledge.Step) string {
	d := s.ID + " [" + s.Kind + "]"
	if s.Cmd != "" {
		d += " " + s.Cmd
	}
	if len(s.Args) > 0 {
		d += " " + strings.Join(s.Args, " ")
	}
	return d
}

func planLine(s planStepOut) string {
	l := describeStep(knowledge.Step{ID: s.ID, Kind: s.Kind, Cmd: s.Cmd, Args: s.Args})
	if s.Service != "" {
		l += " (service " + s.Service + ")"
	}
	if s.Kind == knowledge.KindHost {
		if s.Port > 0 {
			l += fmt.Sprintf(" port: %d", s.Port)
		} else {
			l += " port: unknown"
		}
	}
	l += " - " + s.State
	if s.Trust != "" {
		l += ", " + s.Trust
	}
	return l
}
