//go:build !windows

package state

import "strings"

// cmdlineMatches reports whether the live command line of pid equals the
// recorded one, ignoring whitespace differences.
func cmdlineMatches(pid int, recorded []string) bool {
	got, err := procCmdline(pid)
	return err == nil && strings.Join(strings.Fields(got), " ") == strings.Join(strings.Fields(strings.Join(recorded, " ")), " ")
}
