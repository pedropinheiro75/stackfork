//go:build windows

package state

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	errLockViolation        = syscall.Errno(33)
)

// ErrLocked is what TryLock returns when another holder has the lock.
var ErrLocked = errors.New("lock is held")

// TryLock takes the exclusive lock without waiting (LOCKFILE_FAIL_IMMEDIATELY).
// It returns ErrLocked when someone else holds it.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var ol syscall.Overlapped
	r, _, e := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		f.Close()
		if errors.Is(e, errLockViolation) {
			return nil, ErrLocked
		}
		return nil, e
	}
	return &Lock{file: f}, nil
}

// Held reports whether another holder has an exclusive lock on path right
// now: a shared, immediate lock attempt that is dropped at once. A missing
// file or any error reads as not held.
func Held(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	var ol syscall.Overlapped
	r, _, e := procLockFileEx.Call(f.Fd(), lockfileFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return errors.Is(e, errLockViolation)
	}
	var ul syscall.Overlapped
	procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ul)))
	return false
}

// Lock is a held exclusive lock on a state directory's .lock file.
type Lock struct {
	file *os.File
}

// AcquireLock blocks until it holds an exclusive LockFileEx lock on path.
// No O_APPEND: on Windows Go then drops GENERIC_WRITE and LockFileEx fails with access denied.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
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
