package guard

import (
	"slices"
	"strings"
)

// LiveMessage is shown when an agent tries to turn live mode or automatic
// cleanup on (add-live-mode D12, D21).
const LiveMessage = "stackfork: live mode and automatic cleanup run commits and delete data on this machine; ask the user to run it (agents may run `stackfork live status` and `stackfork live off`)"

// liveForbidden reports the people-only live forms: `live on`, `live cleanup
// auto` and `live cleanup --delete-branch`. `live off`, `live status`,
// `live cleanup off|suggest` and `--keep-branch` stay allowed: they only
// remove automation. Global flags may sit anywhere, and wrappers, `sh -c`
// and `.exe` are seen through by guardSegments. A guardrail, not a sandbox.
func liveForbidden(command, cwd string) bool {
	for _, seg := range guardSegments(command, cwd) {
		w := seg.Words
		if progBase(w[0]) != "stackfork" {
			continue
		}
		var pos, flags []string
		for i := 1; i < len(w); i++ {
			a := w[i]
			if strings.HasPrefix(a, "-") {
				name, _, hasEq := strings.Cut(a, "=")
				if globalValueFlags[name] && !hasEq {
					i++ // its value
					continue
				}
				flags = append(flags, strings.TrimLeft(name, "-"))
				continue
			}
			pos = append(pos, a)
		}
		if len(pos) < 2 || pos[0] != "live" {
			continue
		}
		switch pos[1] {
		case "on":
			return true
		case "cleanup":
			if slices.Contains(flags, "delete-branch") || slices.Contains(pos[2:], "auto") {
				return true
			}
		}
	}
	return false
}
