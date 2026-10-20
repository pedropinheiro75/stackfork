package secretstore

import (
	"fmt"
	"strings"
)

// readOP runs `op read --no-newline REF` (D3). The reference was validated
// by ParseRef, so it is one plain argument.
func (b *Backend) readOP(ref string) (string, Status, error) {
	tool, err := b.opTool()
	if err != nil {
		return "", Unavailable, fmt.Errorf("1Password: %w", err)
	}
	r := runTool(tool, []string{"read", "--no-newline", ref}, "", toolEnv(b.goos(), true), b.timeout(opTimeout))
	if r.err != nil {
		return "", Error, fmt.Errorf("op: %v", r.err)
	}
	if r.exit != 0 {
		low := strings.ToLower(r.stderr)
		switch {
		case strings.Contains(low, "isn't an item"), strings.Contains(low, "isn't a vault"), strings.Contains(low, "could not find"):
			return "", Missing, fmt.Errorf("1Password has no such item or field")
		case strings.Contains(low, "not signed in"), strings.Contains(low, "authorization"), strings.Contains(low, "locked"):
			return "", Locked, fmt.Errorf("1Password is locked or not signed in")
		}
		return "", Error, fmt.Errorf("op exited %d: %s", r.exit, firstLine(r.stderr))
	}
	if r.stdout == "" {
		return "", Missing, fmt.Errorf("1Password returned an empty value")
	}
	return r.stdout, Resolves, nil
}
