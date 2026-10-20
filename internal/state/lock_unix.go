//go:build !windows

package state

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked is what TryLock returns when another holder has the lock.
var ErrLocked = errors.New("lock is held")

// TryLock takes the exclusive lock without waiting (LOCK_EX|LOCK_NB). It
// returns ErrLocked when someone else holds it.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{file: f}, nil
}

// Held reports whether another holder has an exclusive lock on path right
// now. It tries a shared, non-blocking lock and drops it at once. A missing
// file or any error reads as not held.
func Held(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

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
