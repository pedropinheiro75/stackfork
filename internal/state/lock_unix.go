//go:build !windows

package state

import (
	"os"
	"syscall"
)

// Lock is a held exclusive flock on a state directory's .lock file.
type Lock struct {
	file *os.File
}

// AcquireLock mirrors Python's `with (state / '.lock').open('a') as lock:
// fcntl.flock(lock, fcntl.LOCK_EX)`. Blocks until the lock is available.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Release unlocks and closes the lock file.
func (l *Lock) Release() error {
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
