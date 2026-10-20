package lifecycle

import (
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// NeedsTrust reports whether mode runs at least one host step. Only then is
// this machine's trust required; the hash still covers every host and
// prepare step, so one approval covers both modes.
func NeedsTrust(p knowledge.Plan, mode string) bool {
	run, _ := p.ForMode(mode)
	for _, s := range run {
		if s.Kind == knowledge.KindHost {
			return true
		}
	}
	return false
}

// TrustedHere reports whether this machine may run the plan in mode: true
// when the mode runs no host step, else when the trust file matches. diff is
// what changed since the last approval (everything, if never trusted).
func TrustedHere(stateBase, mainRoot string, p knowledge.Plan, mode string) (trusted bool, diff []string) {
	if !NeedsTrust(p, mode) {
		return true, nil
	}
	return HashTrusted(stateBase, mainRoot, p)
}

// HashTrusted ignores the mode: the trust file matches every host and
// prepare step of the plan. A plan without a host step needs no trust.
func HashTrusted(stateBase, mainRoot string, p knowledge.Plan) (trusted bool, diff []string) {
	if len(p.HostSteps()) == 0 {
		return true, nil
	}
	path := knowledge.TrustPath(stateBase, mainRoot)
	if h, _, ok := knowledge.ReadTrust(path); ok && h == knowledge.PlanHash(p.Steps) {
		return true, nil
	}
	return false, knowledge.DiffSteps(knowledge.ReadTrustSteps(path), knowledge.TrustSteps(p.Steps))
}

// ReadableDiff is HashTrusted's diff as lines a person can read: each added
// or changed step with all its fields (StepBlock), removed ones by id.
func ReadableDiff(stateBase, mainRoot string, p knowledge.Plan) []string {
	old := map[string]knowledge.Step{}
	for _, s := range knowledge.ReadTrustSteps(knowledge.TrustPath(stateBase, mainRoot)) {
		old[s.ID] = s
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range knowledge.TrustSteps(p.Steps) {
		seen[s.ID] = true
		o, had := old[s.ID]
		if had && strings.Join(StepBlock(o), "\n") == strings.Join(StepBlock(s), "\n") {
			continue
		}
		mark := "+ "
		if had {
			mark = "~ "
		}
		b := StepBlock(s)
		b[0] = mark + b[0]
		out = append(out, b...)
	}
	for id, o := range old {
		if !seen[id] {
			out = append(out, "- "+id+" ["+o.Kind+"]")
		}
	}
	return out
}
