package dashboard

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// chipView is one StatusChip: a state tone, a glyph and a word (never colour
// alone). app.js's stateChip/serviceChip must produce the same values.
type chipView struct{ Class, Glyph, Label, Title string }

const (
	glyphOK        = "●" // ●
	glyphPartial   = "◐" // ◐
	glyphAttention = "▲" // ▲
	glyphFailed    = "⊘" // ⊘
	glyphBusy      = "◔" // ◔
	glyphOff       = "○" // ○
)

// stateChip is an instance's chip. Stopped is off (grey): failed is only for
// something broken.
func stateChip(state string) chipView {
	switch state {
	case inventory.StateRunning:
		return chipView{Class: "ok", Glyph: glyphOK, Label: "Running"}
	case inventory.StatePartial:
		return chipView{Class: "attention", Glyph: glyphPartial, Label: "Partial"}
	case inventory.StateStopped:
		return chipView{Class: "off", Glyph: glyphOff, Label: "Stopped"}
	case inventory.StateMissingCheckout:
		return chipView{Class: "failed", Glyph: glyphFailed, Label: "Checkout missing"}
	case inventory.StateMissingBranch:
		return chipView{Class: "failed", Glyph: glyphFailed, Label: "Branch missing"}
	}
	return chipView{Class: "attention", Glyph: "?", Label: "Unknown"}
}

// serviceChip is a service's chip: health decides a running service's tone.
func serviceChip(s inventory.Service) chipView {
	c := chipView{Class: "off", Glyph: glyphOff, Title: dotTitle(s)}
	switch {
	case s.State == "running" && s.Health == "unhealthy":
		c.Class, c.Glyph, c.Label = "attention", glyphAttention, "Unhealthy"
	case s.State == "running" && s.Health == "health: starting":
		c.Class, c.Glyph, c.Label = "busy", glyphBusy, "Starting…"
	case s.State == "running":
		c.Class, c.Glyph, c.Label = "ok", glyphOK, "Running"
	case s.State == "dead":
		c.Class, c.Glyph, c.Label = "failed", glyphFailed, "Dead"
	case s.State == "restarting":
		c.Class, c.Glyph, c.Label = "attention", glyphAttention, "Restarting"
	case s.State == inventory.StateWaiting:
		c.Class, c.Glyph, c.Label = "busy", glyphBusy, "Waiting"
	case s.State == inventory.StateNotStarted:
		c.Label = "Not started"
	case s.State == inventory.ServiceOptional:
		c.Label = "Optional"
	case s.State == "exited" || s.State == "stopped":
		c.Label = "Stopped"
	case s.State == "":
		c.Label = "Unknown"
	default:
		c.Label = strings.ToUpper(s.State[:1]) + s.State[1:]
	}
	return c
}

func dotTitle(s inventory.Service) string {
	switch {
	case s.State == inventory.ServiceOptional:
		return "not started (optional)"
	case s.State == inventory.StateNotStarted:
		return "not started"
	case s.State == inventory.StateWaiting:
		return "waiting for " + strings.Join(s.WaitingFor, ", ") + " to become healthy"
	case s.State == "running" && s.Health == "unhealthy":
		return "running, unhealthy"
	case s.State == "running" && s.Health == "health: starting":
		return "running, health check starting"
	}
	return s.State
}

// knowledgeTone: a draft or stale setup needs attention; the rest is quiet.
func knowledgeTone(status string) string {
	if status == "draft" || status == "stale" {
		return "attention"
	}
	return "off"
}

var hashSuffix = regexp.MustCompile(`-[0-9a-f]{12}$`)

// displayName is the project slug without its instance prefix and trailing
// hash: "sf-acme-web-2ffa6841c75d" reads "acme-web". app.js's displayName
// must match.
func displayName(project string) string {
	if n := hashSuffix.ReplaceAllString(projectSlug(project), ""); n != "" {
		return n
	}
	return project
}

// lane maps the 0..7 colour index to the lane-1..8 class.
func lane(i int) int { return i + 1 }

// plural is "1 leftover one-off container" / "2 leftover one-off containers".
func plural(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// runnable mirrors app.js's runnable: the page can run it (the server
// allowlists again); a placeholder keeps it copy-only.
var (
	runnableCmd = regexp.MustCompile(`^(stackfork|git|docker) `)
	placeholder = regexp.MustCompile(`[<>\x{2026}]|\b[A-Z]{3,}\b`)
)

func runnable(cmd string) bool { return runnableCmd.MatchString(cmd) && !placeholder.MatchString(cmd) }

// openApp is the app the card's Open App button opens: the first running
// app link with a URL.
func openApp(apps []inventory.AppLink) *inventory.AppLink {
	for i := range apps {
		if apps[i].URL != "" && apps[i].Running {
			return &apps[i]
		}
	}
	return nil
}

// warningCommands are the commands the warnings name, minus the down
// command the menu already lists (cliCommands' tail).
func warningCommands(cmds []string) []string {
	if len(cmds) > 1 {
		return cmds[1:]
	}
	return nil
}

// endpointNote is the "(stopped)" / "(not started)" / "(proxy)" suffix of an
// endpoint. app.js's endpointNote must match.
func endpointNote(state string, proxied bool) string {
	var parts []string
	if state != "running" {
		if state == inventory.ServiceOptional || state == inventory.StateNotStarted || state == inventory.StateWaiting {
			parts = append(parts, "(not started)")
		} else {
			parts = append(parts, "(stopped)")
		}
	}
	if proxied {
		parts = append(parts, "(proxy)")
	}
	return strings.Join(parts, " ")
}
