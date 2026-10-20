//go:build !windows

package dashboard

import "syscall"

func newConsoleAttr() *syscall.SysProcAttr { return nil }
