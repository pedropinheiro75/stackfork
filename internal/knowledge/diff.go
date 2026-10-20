package knowledge

import "encoding/json"

// DiffSteps lists what changed from old to new: "+ id: step", "- id: step",
// "~ id: old → new". Steps are compared by their JSON form.
func DiffSteps(old, new []Step) []string {
	enc := func(s Step) string { b, _ := json.Marshal(s); return string(b) }
	oldBy := map[string]string{}
	for _, s := range old {
		oldBy[s.ID] = enc(s)
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range new {
		seen[s.ID] = true
		o, had := oldBy[s.ID]
		switch n := enc(s); {
		case !had:
			out = append(out, "+ "+s.ID+": "+n)
		case o != n:
			out = append(out, "~ "+s.ID+": "+o+" → "+n)
		}
	}
	for _, s := range old {
		if !seen[s.ID] {
			out = append(out, "- "+s.ID+": "+oldBy[s.ID])
		}
	}
	return out
}
