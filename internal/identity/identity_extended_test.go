package identity

import "testing"

func TestTrimExtended(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\y`:          `C:\y`,
		`\\?\UNC\srv\share`: `\\srv\share`,
		`C:\y`:              `C:\y`,
		`/home/me/x`:        `/home/me/x`,
	} {
		if got := trimExtended(in); got != want {
			t.Errorf("trimExtended(%q) = %q, want %q", in, got, want)
		}
	}
}
