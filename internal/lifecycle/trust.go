package lifecycle

import "github.com/pedropinheiro75/stackfork/internal/knowledge"

// TrustedHere reports whether this machine trusted the plan's host and
// prepare steps; diff is what changed since (everything, if never trusted).
// A plan without a host step needs no trust.
func TrustedHere(stateBase, mainRoot string, p knowledge.Plan) (trusted bool, diff []string) {
	if len(p.HostSteps()) == 0 {
		return true, nil
	}
	path := knowledge.TrustPath(stateBase, mainRoot)
	if h, _, ok := knowledge.ReadTrust(path); ok && h == knowledge.PlanHash(p.Steps) {
		return true, nil
	}
	return false, knowledge.DiffSteps(knowledge.ReadTrustSteps(path), knowledge.TrustSteps(p.Steps))
}
