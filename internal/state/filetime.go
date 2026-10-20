package state

import (
	"fmt"
	"strconv"
)

// formatFiletime renders a Windows FILETIME (100 ns ticks since 1601) as the
// "filetime:N" start identity.
func formatFiletime(high, low uint32) string {
	return "filetime:" + strconv.FormatUint(uint64(high)<<32|uint64(low), 10)
}

// refusePID rejects pids that must never be signalled: 0 and the Windows
// System/Idle range (<= 4), and the calling process.
func refusePID(pid, self int) error {
	if pid <= 4 || pid == self {
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	return nil
}
