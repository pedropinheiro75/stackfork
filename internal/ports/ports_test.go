package ports

import (
	"net"
	"testing"
)

func TestAllocateDeterministic(t *testing.T) {
	rng := Range{Start: 100, End: 199}
	never := func(int) bool { return false }
	a, err := Allocate("sf-app-aaaaaaaaaaaa", "web", 3000, rng, never)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Allocate("sf-app-aaaaaaaaaaaa", "web", 3000, rng, never)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("expected deterministic allocation, got %d then %d", a, b)
	}
}

func TestAllocateSkipsTakenPorts(t *testing.T) {
	rng := Range{Start: 100, End: 199}
	first, err := Allocate("sf-app-aaaaaaaaaaaa", "web", 3000, rng, func(int) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	taken := map[int]bool{first: true}
	second, err := Allocate("sf-app-aaaaaaaaaaaa", "web", 3000, rng, func(p int) bool { return taken[p] })
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("expected a different port once %d is recorded taken", first)
	}
	if second < rng.Start || second > rng.End {
		t.Fatalf("allocated port %d outside range %+v", second, rng)
	}
}

// TestAllocateDistinctAcrossInstances mirrors design.md's "two instances
// never share a port" scenario: two worktrees of the same project have
// different full project names (the hash includes checkout+branch), so they
// must land on different ports even though neither is running.
func TestAllocateDistinctAcrossInstances(t *testing.T) {
	rng := Range{Start: 100, End: 199}
	never := func(int) bool { return false }
	a, err := Allocate("sf-app-aaaaaaaaaaaa", "web", 3000, rng, never)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Allocate("sf-app-bbbbbbbbbbbb", "web", 3000, rng, never)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("expected distinct ports for two instances of one project, both got %d", a)
	}
}

func TestAllocateExhaustedRangeErrors(t *testing.T) {
	rng := Range{Start: 100, End: 100}
	if _, err := Allocate("sf-app-aaaaaaaaaaaa", "web", 3000, rng, func(int) bool { return true }); err == nil {
		t.Fatal("expected an error when every port in the range is taken")
	}
}

func TestBoundReportsAnOccupiedPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if !Bound(port) {
		t.Fatalf("expected port %d to be reported bound", port)
	}
}

func TestBoundReportsAFreePort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if Bound(port) {
		t.Fatalf("expected port %d to be reported free after closing", port)
	}
}
