package dashboard_test

import "github.com/pedropinheiro75/stackfork/internal/knowledge"

func verifiedFixture(k *knowledge.Knowledge) *knowledge.Knowledge {
	if k.Status == knowledge.StatusVerified {
		if k.Verification == nil {
			k.Verification = &knowledge.Verification{}
		}
		k.Verification.RecipeHash = k.Plan.ExecutionHash()
		k.Verification.RunID = 1
		k.Verification.Mode = knowledge.ModeContainer
	}
	return k
}
