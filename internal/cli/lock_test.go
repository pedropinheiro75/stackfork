package cli

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// cmdUpDownRunExec's up/down/restart/start/stop actions must take the same
// per-instance flock prepare already does (task 2, design.md), before doing
// anything else: whatever lifecycle.Execute goes on to do (it will fail here
// since instanceDir has no real manifest/compose snapshot, docker isn't
// involved), the action must block until an externally-held lock on the same
// instanceDir/.lock file is released.
func TestUpDownActionsBlockOnHeldInstanceLock(t *testing.T) {
	instanceDir := t.TempDir()

	held, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		var stdout, stderr bytes.Buffer
		cmdUpDownRunExec(&stdout, &stderr, "down", nil, instanceDir, "main", "proj", instanceDir, instanceDir, instanceDir) //nolint:errcheck // only the blocking behaviour is under test
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("expected down to block while the instance lock is held")
	case <-time.After(150 * time.Millisecond):
	}

	if err := held.Release(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expected down to proceed once the instance lock was released")
	}
}
