//go:build windows

package dashboard

import "syscall"

// newConsoleAttr gives the child its own visible console window.
func newConsoleAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x10} // CREATE_NEW_CONSOLE
}
