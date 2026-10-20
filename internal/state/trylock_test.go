package state

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTryLockAndHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".run.lock")
	if Held(path) {
		t.Fatal("Held on a missing file must be false")
	}
	l, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	if !Held(path) {
		t.Fatal("Held must be true while held")
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if Held(path) {
		t.Fatal("Held must be false after release")
	}
	l2, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	l2.Release()
}

func TestTryLockNotInheritedByChild(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".run.lock")
	l, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Skip("no sleep command:", err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	l.Release()
	if Held(path) {
		t.Fatal("a live child must not keep the lock")
	}
}
