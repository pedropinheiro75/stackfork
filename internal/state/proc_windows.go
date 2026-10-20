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
	stillActive                    = 259
	detachedProcess                = 0x00000008
)

// DetachAttr starts a child in its own process group with no console, so it
// outlives the caller.
func DetachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
		HideWindow:    true,
	}
}

// KillGroup kills the process tree rooted at pid. The signal is ignored.
// ponytail: a detached supervisor has no console, so no graceful stop; taskkill /T /F always. Upgrade path: Job Object plus a console.
func KillGroup(pid int, sig syscall.Signal) error {
	if err := refusePID(pid, os.Getpid()); err != nil {
		return err
	}
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill %d: %w: %s", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// KillOrphanGroup is a no-op: taskkill /T already took the tree.
// ponytail: children of a supervisor killed first survive; doctor reports their port.
func KillOrphanGroup(pgid int) error { return nil }

// ProcStart returns "filetime:N", the process creation time. Error when pid
// does not exist or has exited.
func ProcStart(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("no such process")
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return "", errors.New("no such process")
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return "", errors.New("no such process") // exited, handle still opens
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	return formatFiletime(creation.HighDateTime, creation.LowDateTime), nil
}

// processAlive: the PID exists AND it was created at startedAt.
func processAlive(pid int, startedAt string) bool {
	if startedAt == "" {
		return false
	}
	got, err := ProcStart(pid)
	return err == nil && got == startedAt
}
