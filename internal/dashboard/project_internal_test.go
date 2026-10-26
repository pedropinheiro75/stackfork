package dashboard

import (
	"strings"
	"testing"
)

func TestTailLinesCapsLinesAndBytes(t *testing.T) {
	if got := tailLines("a\nb\nc\n", 2); got != "b\nc" {
		t.Errorf("lines: %q", got)
	}
	long := strings.Repeat("x", 500)
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString(long + "\n")
	}
	got := tailLines(b.String(), 30)
	if len(got) > logTailBytes || !strings.HasSuffix(got, long) || strings.HasPrefix(got, "\n") {
		t.Errorf("len %d, must fit %d and end on the last line", len(got), logTailBytes)
	}
	one := tailLines(strings.Repeat("y", 3*logTailBytes), 30) // one huge line
	if len(one) != logTailBytes {
		t.Errorf("single line len %d", len(one))
	}
}
