package guard

import "testing"

// Forms the CLI accepts (single-dash global flags, a flag glued with =, an
// action word that is not first, a flag before the action) must be seen by the
// guard the way the CLI sees them. One table over every stackfork rule.
func TestGuardSeesTheFormsTheCLIAccepts(t *testing.T) {
	for cmd, want := range map[string]bool{
		// live
		"stackfork -root /x live on":                   true,
		"stackfork -root=/x live on":                   true,
		"stackfork -state-dir /s live cleanup auto":    true,
		"stackfork on live":                            true,
		"stackfork clean live br --yes":                true,
		"stackfork cleanup live auto":                  true,
		"stackfork cleanup live suggest --keep-branch": false,
		"stackfork clean live br":                      false,
		"stackfork -root /x live status":               false,
		"stackfork status live on":                     false, // the CLI runs status here
		// prune, gc
		"stackfork -root /x prune --yes":     true,
		"stackfork -state-dir=/s prune -y":   true,
		"stackfork --yes prune":              true,
		"stackfork -yes prune":               true,
		"stackfork prune -root /x --all":     true,
		"stackfork apply gc":                 true,
		"stackfork -root /x gc --apply":      true,
		"stackfork --apply gc":               true,
		"stackfork -root /x gc":              false,
		"stackfork -root /x prune --stopped": false,
		// plan --trust, seed, start-env
		"stackfork -instance x plan -trust":         true,
		"stackfork --trust plan":                    true,
		"stackfork -root=/x seed -force":            true,
		"stackfork --force seed":                    true,
		"stackfork -root /x start-env --force x":    true,
		"stackfork -root /x seed":                   false,
		"stackfork prepare --force -f compose.yaml": true,
		"stackfork up --force":                      true,
		"stackfork --root x up --force":             true,
		"stackfork --force up":                      true,
		"stackfork -root=/x prepare -force":         true,
		"stackfork prepare -f compose.yaml":         false,
		"stackfork up --wait-timeout 10":            false,
		// reset
		"stackfork -root /x reset":    true,
		"stackfork -instance=x reset": true,
		// secret
		"stackfork -state-dir /s secret set x":    true,
		"stackfork -root=/x secret map . A=env:B": true,
		"stackfork set secret x":                  true,
		"stackfork -root /x secret ls":            false,
	} {
		if got := Destructive(cmd, "/repo"); got != want {
			t.Errorf("Destructive(%q) = %v, want %v", cmd, got, want)
		}
	}
}
