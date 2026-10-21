//go:build darwin

package identity

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

const fGetPath = 50 // F_GETPATH from <sys/fcntl.h>

// trueCase returns the spelling of an existing path as it is on disk. On a
// case-insensitive volume /Users/x/work and /Users/x/Work are one folder;
// stackfork compares paths as text, so it keeps only the real spelling.
// Any error keeps p unchanged.
func trueCase(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return p
	}
	defer f.Close()
	buf := make([]byte, 1024) // MAXPATHLEN
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), fGetPath, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return p
	}
	if i := bytes.IndexByte(buf, 0); i > 0 {
		return string(buf[:i])
	}
	return p
}
