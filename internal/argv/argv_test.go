package argv

import (
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	g, a, sub, un := Split([]string{"-root=/x", "on", "--state-dir", "/s", "live", "-secret", "A=env:B", "--yes"})
	if !slices.Equal(g, []string{"-root=/x", "--state-dir", "/s", "-secret", "A=env:B"}) || a != "live" || !slices.Equal(sub, []string{"on", "--yes"}) || un != "on" {
		t.Fatalf("%v %q %v %q", g, a, sub, un)
	}
	if _, a, sub, _ := Split([]string{"status", "live", "on"}); a != "status" || len(sub) != 2 {
		t.Fatalf("the first action word wins: %q %v", a, sub)
	}
	if _, a, _, _ := Split([]string{"-h"}); a != "-h" {
		t.Fatalf("help spelling: %q", a)
	}
}
