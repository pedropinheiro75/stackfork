//go:build !windows

package secretstore

import (
	"os"
	"syscall"
)

func ownerOK(_ string, fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	uid := uint32(os.Geteuid())
	return st.Uid == 0 || st.Uid == uid
}
