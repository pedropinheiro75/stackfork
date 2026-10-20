//go:build windows

package state

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const (
	processQueryLimitedInformation = 0x1000
	synchronize                    = 0x00100000
	createNoWindow                 = 0x08000000
)

// DetachAttr starts a child in its own process group with a hidden console,
// so it outlives the caller and its console children (npm.cmd, node) inherit
// that console instead of opening visible windows.
func DetachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow}
}

// KillGroup kills the process tree rooted at pid. The signal is ignored.
// ponytail: a detached supervisor has no console, so no graceful stop; taskkill /T /F always. Upgrade path: Job Object plus a console.
func KillGroup(pid int, sig syscall.Signal) error {
	if err := refusePID(pid, os.Getpid()); err != nil {
		return err
	}
	return taskkill(pid)
}

func taskkill(pid int) error {
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill %d: %w: %s", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// KillHost kills proc's tree only while a handle we hold confirms it is still
// the recorded process (same creation time, still running). An open handle
// pins the PID, so it cannot be recycled between that check and taskkill.
func KillHost(proc HostProc, sig syscall.Signal) error {
	if err := refusePID(proc.PID, os.Getpid()); err != nil {
		return err
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation|synchronize, false, uint32(proc.PID))
	if err != nil {
		return errors.New("no such process")
	}
	defer syscall.CloseHandle(h)
	got, err := handleStart(h)
	if err != nil || got != proc.StartedAt {
		return fmt.Errorf("pid %d is no longer the recorded process; not killing it", proc.PID)
	}
	return taskkill(proc.PID)
}

// KillOrphanGroup is a no-op: taskkill /T already took the tree.
// ponytail: children of a supervisor killed first survive; doctor reports their port.
func KillOrphanGroup(pgid int) error { return nil }

// handleStart returns "filetime:N" for an open handle, error when the process
// has exited (the handle still opens then).
func handleStart(h syscall.Handle) (string, error) {
	if ev, err := syscall.WaitForSingleObject(h, 0); err != nil || ev != syscall.WAIT_TIMEOUT {
		return "", errors.New("no such process")
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	return formatFiletime(creation.HighDateTime, creation.LowDateTime), nil
}

// ProcStart returns "filetime:N", the process creation time. Error when pid
// does not exist or has exited.
func ProcStart(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("no such process")
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation|synchronize, false, uint32(pid))
	if err != nil {
		return "", errors.New("no such process")
	}
	defer syscall.CloseHandle(h)
	return handleStart(h)
}

// processAlive: the PID exists AND it was created at startedAt.
func processAlive(pid int, startedAt string) bool {
	if startedAt == "" {
		return false
	}
	got, err := ProcStart(pid)
	return err == nil && got == startedAt
}
