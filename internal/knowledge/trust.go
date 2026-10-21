package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Trust lives here, not in internal/state: state cannot import knowledge
// (knowledge already imports state for WritePrivate).

type trustFile struct {
	Hash  string `json:"hash"`
	Date  string `json:"date"`
	Steps []Step `json:"steps"`
}

// TrustPath is <stateBase>/trust/<sha256 of the resolved main checkout>.json.
func TrustPath(stateBase, mainRoot string) string {
	p := mainRoot
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	sum := sha256.Sum256([]byte(p))
	return filepath.Join(stateBase, "trust", hex.EncodeToString(sum[:])+".json")
}

// TrustSteps are the steps the trust covers: host, prepare and exec steps
// that run on the host, with list
// fields sorted so their order does not matter.
func TrustSteps(steps []Step) []Step {
	var out []Step
	for _, s := range steps {
		if s.Kind != KindHost && s.Kind != KindPrepare && !s.IsHostExec() {
			continue
		}
		s.Modes = sortedCopy(s.Modes)
		s.Args = append([]string(nil), s.Args...) // arg order is meaningful: kept
		s.Exclude = sortedCopy(s.Exclude)
		s.PortEnv = sortedCopy(s.PortEnv)
		out = append(out, s)
	}
	return out
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// PlanHash is the SHA-256 of the canonical JSON of the trust-covered steps.
func PlanHash(steps []Step) string {
	b, _ := json.Marshal(TrustSteps(steps))
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readTrustFile(path string) (trustFile, bool) {
	var f trustFile
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &f) != nil || f.Hash == "" {
		return f, false
	}
	return f, true
}

// ReadTrust returns the trusted hash and the date it was trusted.
func ReadTrust(path string) (hash, date string, ok bool) {
	f, ok := readTrustFile(path)
	return f.Hash, f.Date, ok
}

// ReadTrustSteps returns the steps recorded when trust was given.
func ReadTrustSteps(path string) []Step {
	f, _ := readTrustFile(path)
	return f.Steps
}

// WriteTrust records hash and the trusted steps (0600).
func WriteTrust(path, hash string, steps []Step) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return state.WritePrivate(path, trustFile{Hash: hash, Date: time.Now().UTC().Format("2006-01-02"), Steps: TrustSteps(steps)})
}
