package live

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func pending(digest string) []Change {
	return []Change{{Path: "bun.lock", Category: CatBuild, Digest: digest, Kind: "changed"}}
}

func TestDebounceTwoWritesGiveOneBatchThreeSecondsAfterTheSecond(t *testing.T) {
	d := NewDebouncer()
	var fired []int
	for s := 0; s <= 10; s++ {
		dig := "a"
		if s >= 2 {
			dig = "b" // second write at t=2
		}
		if d.Observe(t0.Add(time.Duration(s)*time.Second), pending(dig)) {
			fired = append(fired, s)
			break // the run moves the baseline, so nothing stays pending
		}
	}
	if fmt.Sprint(fired) != "[5]" {
		t.Fatalf("fired at %v, want [5]", fired)
	}
}

func TestDebounceContinuousWritesFireAtTheCeiling(t *testing.T) {
	d := NewDebouncer()
	var fired []int
	for s := 0; s <= 70; s++ {
		if d.Observe(t0.Add(time.Duration(s)*time.Second), pending(fmt.Sprint("w", s))) {
			fired = append(fired, s)
		}
	}
	if len(fired) == 0 || fired[0] != 60 {
		t.Fatalf("fired at %v, want first at 60", fired)
	}
}

func TestDebounceResetsWhenNothingIsPending(t *testing.T) {
	d := NewDebouncer()
	d.Observe(t0, pending("a"))
	d.Observe(t0.Add(time.Second), nil) // edit undone
	if d.Observe(t0.Add(10*time.Second), pending("a")) {
		t.Fatal("a new first change fired at once")
	}
	if !d.Observe(t0.Add(13*time.Second), pending("a")) {
		t.Fatal("no batch 3 s after the first sight")
	}
}
