package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func (p Plan) ExecutionHash() string {
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s Step) ExecutionHash() string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (k *Knowledge) VerificationCurrent() bool {
	proof := k.Verification
	return proof != nil && proof.RunID > 0 && proof.RecipeHash == k.Plan.ExecutionHash() && k.Plan.SupportsMode(proof.Mode)
}
