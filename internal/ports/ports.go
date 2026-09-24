// Package ports implements the stable host-port allocator from design.md D7
// layer 2: each (project, service, target port) gets a deterministic
// loopback host port from a configurable range, so restarting an instance or
// recreating its containers never changes its URL (task 8.1).
package ports

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"strconv"
)

// DefaultStart and DefaultEnd are the default allocation window (design.md
// D7 layer 2).
const (
	DefaultStart = 20000
	DefaultEnd   = 29999
)

// Range is an inclusive port range candidates are drawn from.
type Range struct {
	Start, End int
}

// RangeFromEnv returns the default range (20000-29999), overridden by
// STACKFORK_PORT_RANGE_START/STACKFORK_PORT_RANGE_END when set, so tests (or
// a user with a crowded range) can point allocation at a private window
// instead of the default one (task 8.1).
func RangeFromEnv() Range {
	r := Range{Start: DefaultStart, End: DefaultEnd}
	if v := os.Getenv("STACKFORK_PORT_RANGE_START"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			r.Start = n
		}
	}
	if v := os.Getenv("STACKFORK_PORT_RANGE_END"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			r.End = n
		}
	}
	return r
}

// seed hashes project/service/target into a deterministic starting offset
// within a range-sized ring (task 8.1): the same triple always probes from
// the same port first.
func seed(project, service string, target int) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s/%s/%d", project, service, target)
	return h.Sum64()
}

// Allocate picks a free host port for (project, service, target) in rng: it
// starts at the deterministic seed offset and probes forward, wrapping once
// around the range, past every port for which taken reports true. taken
// combines "already bound" and "recorded by another manifest" - the caller
// supplies both (task 8.1).
func Allocate(project, service string, target int, rng Range, taken func(port int) bool) (int, error) {
	size := rng.End - rng.Start + 1
	if size <= 0 {
		return 0, fmt.Errorf("invalid port range %d-%d", rng.Start, rng.End)
	}
	offset := int(seed(project, service, target) % uint64(size))
	for i := 0; i < size; i++ {
		port := rng.Start + (offset+i)%size
		if !taken(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d for %s/%s/%d", rng.Start, rng.End, project, service, target)
}

// Bound reports whether 127.0.0.1:port is already in use, by attempting to
// bind it (task 8.1's "probe forward past ports that are in use"; also used
// at `up`, task 8.3, to check a recorded port is still free).
func Bound(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return true
	}
	l.Close()
	return false
}
