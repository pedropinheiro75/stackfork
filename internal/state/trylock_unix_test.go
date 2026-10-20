//go:build !windows

package state

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The holder gives up its file without unlocking while a child it started
// lives: if the child had inherited the descriptor, it would keep the lock.
func TestTryLockNotInheritedByChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".run.lock")
	l, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		l.Release()
		t.Skip("no sleep command:", err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	if !Held(path) {
		t.Fatal("the lock must be held before the holder lets go")
	}
	l.file.Close() // no LOCK_UN: only the last descriptor of the open file releases it
	if Held(path) {
		t.Fatal("a live child must not keep the lock")
	}
}
