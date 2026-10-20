//go:build !windows

package cli

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

var hostUnsupported error

var hostSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// childProcAttr: the foreground form puts the command in its own group so a
// forwarded signal reaches its whole tree. Under the supervisor the command
// stays in the supervisor's group instead, so `host stop` (which signals that
// one group) reaches the command and everything it spawned, even when the
// supervisor itself is killed.
func childProcAttr(supervise bool) *syscall.SysProcAttr {
	if supervise {
		return nil
	}
	return &syscall.SysProcAttr{Setpgid: true}
}

func forwardSignal(p *os.Process, _ string, sig os.Signal, supervise bool) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	if supervise {
		p.Signal(s) //nolint:errcheck // the group signal may have reached it already
		return
	}
	syscall.Kill(-p.Pid, s) //nolint:errcheck // the group may already be gone
}

func exitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return exitErr.ExitCode(), nil
	}
	return 0, err
}
