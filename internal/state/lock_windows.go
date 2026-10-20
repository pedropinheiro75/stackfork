//go:build windows

package state

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x2

// Lock is a held exclusive lock on a state directory's .lock file.
type Lock struct {
	file *os.File
}

// AcquireLock blocks until it holds an exclusive LockFileEx lock on path.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	var ol syscall.Overlapped
	r, _, e := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		f.Close()
		return nil, e
	}
	return &Lock{file: f}, nil
}

// Release unlocks and closes the lock file.
func (l *Lock) Release() error {
	var ol syscall.Overlapped
	r, _, e := procUnlockFileEx.Call(l.file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	closeErr := l.file.Close()
	if r == 0 {
		return e
	}
	return closeErr
}
