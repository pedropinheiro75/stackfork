//go:build windows

package repos

import (
	"strings"
	"testing"
)

// The macOS /private trim must not touch a Windows path.
func TestPrivateTrimLeavesWindowsPath(t *testing.T) {
	src := `C:\private\x`
	if got := strings.TrimPrefix(src, "/private"); got != src {
		t.Fatalf("got %q", got)
	}
}
