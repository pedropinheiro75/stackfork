package state

import (
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/ports"
)

// HostIdentity reports whether proc is still the process that was recorded:
// the PID is alive, started at StartedAt, and runs the recorded command line.
// A PID alone is never enough. Safe to use before signalling.
func HostIdentity(proc HostProc) bool {
	if !processAlive(proc.PID, proc.StartedAt) {
		return false
	}
	return cmdlineMatches(proc.PID, proc.Cmdline)
}

// HostStatus is running only when the identity holds AND the port answers.
func HostStatus(instanceDir, name string) (running bool, proc HostProc) {
	m, err := ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil || m == nil {
		return false, HostProc{}
	}
	proc = m.HostProcs[name]
	return proc.PID != 0 && HostIdentity(proc) && ports.Listening(proc.Port), proc
}

// HostAlive is the PID-plus-start-time check alone (no command line, no port).
func HostAlive(proc HostProc) bool { return processAlive(proc.PID, proc.StartedAt) }
