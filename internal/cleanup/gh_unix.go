//go:build !windows

package cleanup

import (
	"os"
	"syscall"
)

// ownerOK: root or the current user.
func ownerOK(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && (st.Uid == 0 || int(st.Uid) == os.Getuid())
}
