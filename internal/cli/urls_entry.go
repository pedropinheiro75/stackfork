package cli

import (
	"fmt"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// entryURLLines are the `urls` lines for the resolved entry and links (D11),
// printed before the endpoint lines, and the warnings for targets the stack
// lacks. A target whose port is not published yet has no line.
func entryURLLines(r knowledge.Resolved, w knowledge.Where) (lines, warnings []string) {
	if r.OK {
		if url, _ := r.AppURL(w); url != "" {
			note := fmt.Sprintf("entry %s:%d", r.Service, r.Port)
			switch r.Source {
			case knowledge.SourceHostRoute:
				note = "entry via the host route"
			case knowledge.SourceGuessed:
				note = fmt.Sprintf("guessed: %s:%d; record the entry with stackfork learn record entry", r.Service, r.Port)
			}
			lines = append(lines, fmt.Sprintf("%s: %s (%s)", knowledge.ReservedLinkName, url, note))
		}
	} else if r.Warning != "" {
		warnings = append(warnings, r.Warning)
	}
	for _, l := range r.Links {
		if !l.OK {
			warnings = append(warnings, l.Warning)
			continue
		}
		url, _ := r.LinkURL(l, w)
		switch {
		case url == "":
		case l.OnEntry:
			lines = append(lines, fmt.Sprintf("%s: %s", l.Name, url))
		default:
			lines = append(lines, fmt.Sprintf("%s: %s (%s:%d)", l.Name, url, l.Service, l.Port))
		}
	}
	return lines, warnings
}
