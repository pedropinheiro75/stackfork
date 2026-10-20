//go:build windows

package cli

import (
	"errors"
	"os"
	"syscall"
)

var hostUnsupported = errors.New("host processes are not supported on Windows yet")

var hostSignals = []os.Signal{os.Interrupt}

func childProcAttr(bool) *syscall.SysProcAttr { return nil }

func forwardSignal(p *os.Process, _ os.Signal, _ bool) { p.Kill() }

func exitCode(err error) (int, error) { return 1, err }
