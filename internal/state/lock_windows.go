//go:build windows

package state

import "errors"

// Lock is a held exclusive lock on a state directory's .lock file.
type Lock struct{}

// AcquireLock is not available on Windows yet.
func AcquireLock(path string) (*Lock, error) {
	return nil, errors.New("instance locks are not supported on Windows yet")
}

// Release is a no-op.
func (l *Lock) Release() error { return nil }
