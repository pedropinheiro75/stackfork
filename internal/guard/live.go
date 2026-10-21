package guard

import (
	"slices"
	"strings"
)

// LiveMessage is shown when an agent tries to turn live mode or automatic
// cleanup on (add-live-mode D12, D21).
const LiveMessage = "stackfork: live mode and automatic cleanup run commits and delete data on this machine; ask the user to run it (agents may run `stackfork live status` and `stackfork live off`)"

// liveForbidden reports the people-only live forms: `live on`, `live cleanup
// auto`, `live cleanup --delete-branch` and `live clean --yes`. `live off`, `live status`,
// `live cleanup off|suggest`, `--keep-branch` and `live clean` without `--yes` stay allowed: they only
// remove automation. Global flags may sit anywhere, and wrappers, `sh -c`
// and `.exe` are seen through by guardSegments. A guardrail, not a sandbox.
func liveForbidden(command, cwd string) bool {
	for _, seg := range guardSegments(command, cwd) {
		w := seg.Words
		if progBase(w[0]) != "stackfork" {
			continue
		}
		action, args := stackforkCall(w)
		if action != "live" {
			continue
		}
		var pos, flags []string
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				name, _, _ := strings.Cut(a, "=")
				flags = append(flags, strings.TrimLeft(name, "-"))
				continue
			}
			pos = append(pos, a)
		}
		if len(pos) < 1 {
			continue
		}
		switch pos[0] {
		case "on":
			return true
		case "cleanup":
			if slices.Contains(flags, "delete-branch") || slices.Contains(pos[1:], "auto") {
				return true
			}
		case "clean": // the preview stays allowed; --yes deletes a worktree and its database
			if slices.Contains(flags, "yes") {
				return true
			}
		}
	}
	return false
}
