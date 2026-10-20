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

// HostStopperActive tells a supervisor whether to leave its cleanup to a
// stopper: the flag names the stopper's pid and start time, and counts only
// while that process is alive. A flag left by a stopper that died is removed.
func HostStopperActive(instanceDir, name string) bool {
	flag := HostStopFlag(instanceDir, name)
	b, err := os.ReadFile(flag)
	if err != nil {
		return false
	}
	var pid int
	var started string
	if _, err := fmt.Sscanf(string(b), "%d %s", &pid, &started); err == nil && state.HostAlive(state.HostProc{PID: pid, StartedAt: started}) {
		return true
	}
	os.Remove(flag)
	return false
}

// StopHost stops one host process and removes its route. keep leaves the
// HostProcs entry, so the row stays as exited and Start can respawn it from
// the recorded command line; `down` passes false and forgets it.
// It signals only when the recorded identity (PID, start time, command line)
// still matches, and only the supervisor's own process group. A stale entry
// (dead process, or a recycled PID) is cleaned without signalling anything.
// The caller must hold the instance lock, or be the only writer.
func StopHost(stderr io.Writer, instanceDir, name string, timeout time.Duration, keep bool) error {
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
		if started, err := state.ProcStart(os.Getpid()); err == nil {
			os.WriteFile(flag, []byte(fmt.Sprintf("%d %s\n", os.Getpid(), started)), 0o600) //nolint:errcheck // best effort; without it a supervisor may wait for the lock
		}
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
		// The leader is gone (identity was verified above); a member that
		// ignored SIGTERM may not be.
		if err := state.KillOrphanGroup(proc.PID); err != nil {
			return fmt.Errorf("host process %s: %w", name, err)
		}
	} else if keep {
		fmt.Fprintf(stderr, "stackfork: host process %s is not running\n", name)
	}
	m, err = state.ReadManifest(manifestPath)
	if err != nil || m == nil {
		return err
	}
	if !keep {
		delete(m.HostProcs, name)
	}
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
		if err := StopHost(stderr, instanceDir, n, timeout, false); err != nil {
			fmt.Fprintf(stderr, "stackfork: warning: %v\n", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// StopHostLocked is StopHost under the instance lock, then a proxy reload so
// the removed route disappears. Used by `host stop|restart` and the dashboard.
func StopHostLocked(stderr io.Writer, instanceDir, name, newBase, legacyBase string) error {
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return err
	}
	err = StopHost(stderr, instanceDir, name, HostStopTimeout(), true)
	lock.Release()
	ReloadProxyIfRunning(stderr, newBase, legacyBase)
	return err
}

// HostRestart stops and respawns one recorded host process. The cli package
// sets the real one (it owns the supervisor spawn).
var HostRestart = func(stderr io.Writer, root, instanceDir, name, newBase, legacyBase string) error {
	return errors.New("host restart is not available")
}
