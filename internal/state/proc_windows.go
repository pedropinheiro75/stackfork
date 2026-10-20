//go:build windows

package state

import (
	"errors"
	"syscall"
)

// ErrNotOnWindows is returned by every host-process primitive on Windows.
var ErrNotOnWindows = errors.New("host processes are not supported on Windows yet")

func DetachAttr() *syscall.SysProcAttr            { return nil }
func KillGroup(pid int, sig syscall.Signal) error { return ErrNotOnWindows }
func processAlive(pid int, startedAt string) bool { return false }
func ProcStart(pid int) (string, error)           { return "", ErrNotOnWindows }
func procCmdline(pid int) (string, error)         { return "", ErrNotOnWindows }
