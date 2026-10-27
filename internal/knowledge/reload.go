package knowledge

import "fmt"

// Reload modes: how an edit reaches the running app.
const (
	ReloadHot     = "hot"     // picked up live (a dev server or watcher)
	ReloadRestart = "restart" // code is mounted or on the host, the process needs a restart
	ReloadRebuild = "rebuild" // code is baked into the image
)

// Reload is how code changes reach one app: a Compose service or a host app,
// by name. Proposed marks the scan's guess, which only `learn record reload`
// clears. Evidence is the file:line the guess came from.
type Reload struct {
	Service  string `json:"service"`
	Mode     string `json:"mode"`
	Proposed bool   `json:"proposed,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	Note     string `json:"note,omitempty"`
}

// ValidReloadMode reports whether m is a recordable mode.
func ValidReloadMode(m string) bool {
	return m == ReloadHot || m == ReloadRestart || m == ReloadRebuild
}

// ReloadFor returns the entry for service, or nil.
func (k *Knowledge) ReloadFor(service string) *Reload {
	if k == nil {
		return nil
	}
	for i := range k.Reload {
		if k.Reload[i].Service == service {
			return &k.Reload[i]
		}
	}
	return nil
}

// ReloadText is the chip label and the tooltip for a mode ("" for none).
func ReloadText(mode string, proposed bool) (label, hint string) {
	switch mode {
	case ReloadHot:
		label, hint = "hot reload", "Edits are picked up live. No restart needed."
	case ReloadRestart:
		label, hint = "restart on change", "The code is mounted or on the host, but the process must restart after an edit: use Restart."
	case ReloadRebuild:
		label, hint = "rebuild on change", "The code is baked into the image: after an edit, run Start or stackfork up --build."
	default:
		return "", ""
	}
	if proposed {
		hint += " (Inferred by the scan, not confirmed.)"
	}
	return label, hint
}

// ValidateReload checks the list Save writes.
func ValidateReload(list []Reload) error {
	seen := map[string]bool{}
	for _, r := range list {
		switch {
		case !ValidEntryService(r.Service):
			return fmt.Errorf("reload: %q is not a service or host app name", r.Service)
		case !ValidReloadMode(r.Mode):
			return fmt.Errorf("reload %s: mode must be hot, restart or rebuild", r.Service)
		case seen[r.Service]:
			return fmt.Errorf("reload: %s is listed twice", r.Service)
		}
		seen[r.Service] = true
	}
	return nil
}
