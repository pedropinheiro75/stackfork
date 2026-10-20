package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// HostStopTimeout is how long a host process gets after SIGTERM before the
// group is killed: 10 s, or STACKFORK_HOST_STOP_TIMEOUT (a Go duration).
func HostStopTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("STACKFORK_HOST_STOP_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return 10 * time.Second
}

// HostStopFlag is the file a stopper leaves while it stops name. The
// supervisor skips its own manifest cleanup when it exists, because the
// stopper cleans up (and may hold the instance lock, which would otherwise
// deadlock the supervisor's exit).
func HostStopFlag(instanceDir, name string) string {
	return filepath.Join(instanceDir, "host-"+name+".stopping")
}

// StopHost stops one host process and removes its HostProcs entry and route.
// It signals only when the recorded identity (PID, start time, command line)
// still matches, and only the supervisor's own process group. A stale entry
// (dead process, or a recycled PID) is cleaned without signalling anything.
// The caller must hold the instance lock, or be the only writer.
func StopHost(stderr io.Writer, instanceDir, name string, timeout time.Duration) error {
	manifestPath := filepath.Join(instanceDir, "manifest.json")
	m, err := state.ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	var proc state.HostProc
	var ok bool
	if m != nil {
		proc, ok = m.HostProcs[name]
	}
	if !ok {
		return fmt.Errorf("no host process %s", name)
	}
	if state.HostIdentity(proc) {
		flag := HostStopFlag(instanceDir, name)
		os.WriteFile(flag, nil, 0o600) //nolint:errcheck // best effort; without it a supervisor may wait for the lock
		defer os.Remove(flag)
		if err := state.KillGroup(proc.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("host process %s: %w", name, err)
		}
		if !waitGone(proc, timeout) {
			fmt.Fprintf(stderr, "stackfork: %s ignored SIGTERM for %v; killing it\n", name, timeout)
			if err := state.KillGroup(proc.PID, syscall.SIGKILL); err != nil {
				return fmt.Errorf("host process %s: %w", name, err)
			}
			if !waitGone(proc, 5*time.Second) {
				return fmt.Errorf("host process %s (pid %d) did not exit", name, proc.PID)
			}
		}
	} else {
		fmt.Fprintf(stderr, "stackfork: host process %s is not running (stale entry); cleaning it up\n", name)
	}
	m, err = state.ReadManifest(manifestPath)
	if err != nil || m == nil {
		return err
	}
	delete(m.HostProcs, name)
	delete(m.Routes, name)
	return state.WritePrivate(manifestPath, m)
}

// waitGone polls until proc's PID no longer matches its start time.
func waitGone(proc state.HostProc, timeout time.Duration) bool {
	for end := time.Now().Add(timeout); ; time.Sleep(50 * time.Millisecond) {
		if !state.HostAlive(proc) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
	}
}

// StopAllHosts stops every recorded host process of the instance. A failure
// on one is reported and does not stop the others.
func StopAllHosts(stderr io.Writer, instanceDir string, timeout time.Duration) error {
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil || m == nil {
		return err
	}
	names := make([]string, 0, len(m.HostProcs))
	for n := range m.HostProcs {
		names = append(names, n)
	}
	sort.Strings(names)
	var errs []error
	for _, n := range names {
		if err := StopHost(stderr, instanceDir, n, timeout); err != nil {
			fmt.Fprintf(stderr, "stackfork: warning: %v\n", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
