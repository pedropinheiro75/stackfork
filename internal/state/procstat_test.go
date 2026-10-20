//go:build !windows

package state

import "testing"

func TestParseProcStatHandlesOddCommNames(t *testing.T) {
	// comm "a b) c (d" holds spaces and parens; starttime is field 22.
	stat := "123 (a b) c (d) S 1 123 123 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 100 18446744073709551615"
	st, ticks, err := parseProcStat(stat)
	if err != nil || st != "S" || ticks != "987654" {
		t.Fatalf("got %q %q %v", st, ticks, err)
	}
	if _, _, err := parseProcStat("garbage"); err == nil {
		t.Fatal("garbage must fail")
	}
}
