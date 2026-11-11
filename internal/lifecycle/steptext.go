package lifecycle

import (
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// StepText is the mono text of a row: the command, or what the step is.
func StepText(st knowledge.Step) string {
	var parts []string
	switch st.Kind {
	case knowledge.KindPrepare:
		parts = append(parts, "prepare "+strings.Join(st.Args, " "))
	case knowledge.KindComposeUp:
		if st.Text != "" { // a live run names what it builds
			return st.Text
		}
		parts = append(parts, "compose up")
		if len(st.Exclude) > 0 {
			parts = append(parts, "without "+strings.Join(st.Exclude, ", "))
		}
	case knowledge.KindNote:
		parts = append(parts, st.Text)
	default:
		parts = append(parts, st.Cmd)
		if st.Service != "" {
			parts = append(parts, "in "+st.Service)
		}
		if st.IsHostExec() {
			parts = append(parts, "on host")
		}
		if st.On == knowledge.OnOneOff {
			parts = append(parts, "in a one-off container")
		}
	}
	if st.Kind == knowledge.KindHost {
		if st.Port > 0 {
			parts = append(parts, "port "+strconv.Itoa(st.Port))
		} else {
			parts = append(parts, "port unknown")
		}
	}
	return strings.TrimSpace(strings.Join(parts, "  "))
}
