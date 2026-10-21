package knowledge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/pedropinheiro75/stackfork/internal/compose"
)

// SecretDecl declares a secret NAME the project needs, with an optional
// hint for the person who has to provide it (add-secret-vault D6). It never
// carries a reference or a value: a committed reference could point a name
// at an unrelated credential. References live in the machine config.
type SecretDecl struct {
	Name string `json:"name"`
	Hint string `json:"hint,omitempty"`
}

// ValidateSecrets checks names, hints and duplicates. Errors never print a
// name or hint that failed, since the text may be a value pasted by mistake.
func ValidateSecrets(list []SecretDecl) error {
	seen := map[string]bool{}
	for i, s := range list {
		if why := compose.SecretNameProblem(s.Name); why != "" {
			return fmt.Errorf("secrets[%d]: %s", i, why)
		}
		if strings.ContainsFunc(s.Hint, unicode.IsControl) {
			return fmt.Errorf("secrets[%d] (%s): hint has a control character", i, s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("secrets[%d] (%s): declared twice", i, s.Name)
		}
		seen[s.Name] = true
	}
	return nil
}

// parseSecrets decodes the "secrets" key strictly: a list of objects with
// exactly the keys name and, optionally, hint.
func parseSecrets(raw json.RawMessage) ([]SecretDecl, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("secrets must be a list of {name, hint} objects")
	}
	var out []SecretDecl
	for i, e := range entries {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(e, &obj); err != nil || obj == nil {
			return nil, fmt.Errorf("secrets[%d] must be an object with name and optional hint", i)
		}
		var d SecretDecl
		if v, ok := obj["name"]; ok {
			if err := json.Unmarshal(v, &d.Name); err != nil {
				return nil, fmt.Errorf("secrets[%d]: name must be a string", i)
			}
		}
		label := fmt.Sprintf("secrets[%d]", i)
		if compose.ValidSecretName(d.Name) {
			label += " (" + d.Name + ")"
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k != "name" && k != "hint" {
				return nil, fmt.Errorf("%s: key %q is not allowed; knowledge declares secret names only, references live in the machine config (stackfork secret map)", label, k)
			}
		}
		if v, ok := obj["hint"]; ok {
			if err := json.Unmarshal(v, &d.Hint); err != nil {
				return nil, fmt.Errorf("%s: hint must be a string", label)
			}
		}
		if d.Name == "" {
			return nil, fmt.Errorf("%s: name is required", label)
		}
		out = append(out, d)
	}
	if err := ValidateSecrets(out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertSecret declares name, or updates its hint; an empty hint keeps the
// recorded one.
func (k *Knowledge) UpsertSecret(name, hint string) {
	for i := range k.Secrets {
		if k.Secrets[i].Name == name {
			if hint != "" {
				k.Secrets[i].Hint = hint
			}
			return
		}
	}
	k.Secrets = append(k.Secrets, SecretDecl{Name: name, Hint: hint})
}

// RemoveSecret drops a declared name; it reports whether one was there.
func (k *Knowledge) RemoveSecret(name string) bool {
	for i := range k.Secrets {
		if k.Secrets[i].Name == name {
			k.Secrets = append(k.Secrets[:i:i], k.Secrets[i+1:]...)
			return true
		}
	}
	return false
}
