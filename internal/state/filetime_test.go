package state

import "testing"

func TestFormatFiletime(t *testing.T) {
	if got := formatFiletime(1, 2); got != "filetime:4294967298" {
		t.Fatalf("got %q", got)
	}
	if got := formatFiletime(0, 0); got != "filetime:0" {
		t.Fatalf("got %q", got)
	}
}

func TestRefusePID(t *testing.T) {
	for _, pid := range []int{0, 4, 9} {
		if refusePID(pid, 9) == nil {
			t.Fatalf("pid %d must be refused", pid)
		}
	}
	if err := refusePID(5, 9); err != nil {
		t.Fatal(err)
	}
}
