package lifecycle

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// PlanStep is one step of `stackfork plan --json` and of the Repos panel.
type PlanStep struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind"`
	Cmd     string         `json:"cmd,omitempty"`
	Service string         `json:"service,omitempty"`
	Args    []string       `json:"args,omitempty"`
	Exclude []string       `json:"exclude,omitempty"`
	Name    string         `json:"name,omitempty"`
	Dir     string         `json:"dir,omitempty"`
	PortEnv []string       `json:"port_env,omitempty"`
	Port    int            `json:"port,omitempty"`
	Once    string         `json:"once,omitempty"`
	On      string         `json:"on,omitempty"`
	Text    string         `json:"text,omitempty"`
	Modes   []string       `json:"modes,omitempty"`
	State   string         `json:"state"`
	Step    knowledge.Step `json:"-"` // the original, for StepBlock
	Trust   string         `json:"trust"`
	Source  string         `json:"source,omitempty"` // a derived step: where it comes from
	// Expanded is cmd with its placeholders replaced for the current
	// checkout; ExpandError says why that is not possible (no port yet).
	Expanded    string `json:"expanded,omitempty"`
	ExpandError string `json:"expand_error,omitempty"`
}

// NewPlanStep is the view of one step in the given state.
func NewPlanStep(s knowledge.Step, state string) PlanStep {
	return PlanStep{ID: s.ID, Kind: s.Kind, Cmd: s.Cmd, Service: s.Service, Args: s.Args, Exclude: s.Exclude,
		Name: s.Name, Dir: s.Dir, PortEnv: s.PortEnv, Port: s.Port, Once: s.Once, On: s.On, Text: s.Text, Modes: s.Modes, State: state, Step: s}
}

// StateDerived is the State of a derived step: nobody recorded it.
const StateDerived = "derived"

// DerivedPlan is what a Start of a checkout without recorded plan steps would
// run (add-derived-plan-view): the steps its run declares, each with its
// source, or the refusal Start would print. PrepareFrom names the prepare
// source (manifest, plan, adapter_files, adapter, sibling); Gap is the
// recipe-gap warning of a worktree whose files are fewer than the main
// checkout's last prepare.
type DerivedPlan struct {
	Mode        string     `json:"mode"`
	Refusal     string     `json:"refusal,omitempty"`
	PrepareFrom string     `json:"prepare_from,omitempty"`
	Gap         string     `json:"gap,omitempty"`
	Steps       []PlanStep `json:"steps"`
}

// PlanOut is the plan for one mode. Diff (not in the JSON) is what changed
// in the host and prepare steps since this machine trusted them.
type PlanOut struct {
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	Verified bool   `json:"verified"`
	Trusted  bool   `json:"trusted"` // for this mode: true when the mode runs no host step
	// TrustNeeded: the mode runs a host step, so trust is required.
	TrustNeeded bool       `json:"trust_needed"`
	Steps       []PlanStep `json:"steps"`
	Skipped     []PlanStep `json:"skipped"`
	Diff        []string   `json:"-"`
	// A derived plan (add-derived-plan-view) has Status "derived".
	Derived     bool   `json:"derived,omitempty"`
	PrepareFrom string `json:"prepare_from,omitempty"`
	Refusal     string `json:"refusal,omitempty"`
	Gap         string `json:"gap,omitempty"`
}

// View is the derived plan in the shape of `plan --json`: a plan with no host
// step needs no trust, so it is trusted and needs none.
func (d DerivedPlan) View() PlanOut {
	return PlanOut{Mode: d.Mode, Status: StateDerived, Trusted: true, Steps: append([]PlanStep{}, d.Steps...), Skipped: []PlanStep{},
		Derived: true, PrepareFrom: d.PrepareFrom, Refusal: d.Refusal, Gap: d.Gap}
}

// PlanView computes the plan for mode. The CLI (`plan --json`) and the
// dashboard render this one result, so they cannot disagree.
func PlanView(stateBase, main string, k *knowledge.Knowledge, mode string) PlanOut {
	trusted, _ := TrustedHere(stateBase, main, k.Plan, mode)
	rawTrusted, _ := HashTrusted(stateBase, main, k.Plan)
	run, skipped := k.Plan.ForMode(mode)
	seed, hasSeed := SeedEntry(main, main, "", k.Plan, mode)
	run = RunOrder(run, seed, hasSeed) // execution order, as a Start lists it
	state := k.Plan.Status
	if k.Status == knowledge.StatusVerified {
		state = knowledge.StatusVerified
	}
	conv := func(in []knowledge.Step) []PlanStep {
		out := []PlanStep{}
		for _, s := range in {
			o := NewPlanStep(s, state)
			if s.Kind == knowledge.KindExec && isDraft(k) && k.Plan.Status != knowledge.PlanRecorded {
				switch s.Once {
				case knowledge.OnceData:
					o.Source = DraftSkip("seed", "seed", s.Once)
				case knowledge.OnceSchema:
					o.Source = DraftSkip("migrate", s.ID, s.Once)
				}
			}
			if s.Kind == knowledge.KindHost || s.Kind == knowledge.KindPrepare || s.IsHostExec() {
				o.Trust = "untrusted"
				if rawTrusted {
					o.Trust = "trusted-here"
				}
			}
			out = append(out, o)
		}
		return out
	}
	out := PlanOut{Mode: mode, Status: k.Plan.Status, Verified: k.Status == knowledge.StatusVerified,
		Trusted: trusted, TrustNeeded: NeedsTrust(k.Plan, mode), Steps: conv(run), Skipped: conv(skipped)}
	if !rawTrusted {
		out.Diff = ReadableDiff(stateBase, main, k.Plan)
	}
	return out
}

// TrustHere records this machine's trust in the plan's host and prepare
// steps and returns the steps it covers. Callers ask a person first.
func TrustHere(stateBase, main string, p knowledge.Plan) ([]knowledge.Step, error) {
	covered := knowledge.TrustSteps(p.Steps)
	return covered, knowledge.WriteTrust(knowledge.TrustPath(stateBase, main), knowledge.PlanHash(p.Steps), covered)
}

// ErrTrustPromptTooLong: a dialog nobody can read approves nothing.
var ErrTrustPromptTooLong = errors.New("This plan is too long to review in a dialog; review it with `stackfork plan`, then run `stackfork plan --trust` in a terminal")

const trustPromptMax = 3000

// TrustPrompt is the text of the native dialog: every step the approval
// covers with all its fields, then what changed since the last approval.
// When that text is longer than one dialog can show, it returns
// ErrTrustPromptTooLong: nobody may approve steps they could not see.
func TrustPrompt(p knowledge.Plan, diff []string) (string, error) {
	lines := []string{"Trusts these steps on this machine:", ""}
	for _, s := range knowledge.TrustSteps(p.Steps) {
		lines = append(lines, StepBlock(s)...)
	}
	if len(diff) > 0 {
		lines = append(lines, "", "Changed since last trusted:")
		lines = append(lines, diff...)
	}
	text := strings.Join(lines, "\n")
	if len(text) > trustPromptMax {
		return "", ErrTrustPromptTooLong
	}
	return text, nil
}

// safe shows a value as written unless it holds a control or non-printing
// character, which could fake a line of the dialog: then it is quoted.
func safe(v string) string {
	if strings.IndexFunc(v, func(r rune) bool { return r != ' ' && !unicode.IsPrint(r) }) >= 0 {
		return strconv.Quote(v)
	}
	return v
}

// StepBlock lists every non-empty field of the step (derived from the Step
// struct, so a future field cannot be left out), which is what the trust
// hash covers, one per line.
func StepBlock(s knowledge.Step) []string {
	l := []string{safe(s.ID) + " [" + safe(s.Kind) + "]"}
	v, t := reflect.ValueOf(s), reflect.TypeOf(s)
	for i := 0; i < t.NumField(); i++ {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if !t.Field(i).IsExported() || key == "id" || key == "kind" || v.Field(i).IsZero() ||
			(v.Field(i).Kind() == reflect.Slice && v.Field(i).Len() == 0) {
			continue
		}
		val := ""
		switch f := v.Field(i).Interface().(type) {
		case []string:
			sep := ", "
			if key == "args" {
				sep = " "
			}
			val = strings.Join(f, sep)
		default:
			val = fmt.Sprint(f)
		}
		l = append(l, "    "+key+": "+safe(val))
	}
	return l
}
