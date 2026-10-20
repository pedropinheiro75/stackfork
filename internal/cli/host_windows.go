//go:build windows

package cli

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

var hostUnsupported error

// ponytail: Ctrl-C only; there is no SIGTERM/SIGHUP to catch on Windows.
var hostSignals = []os.Signal{os.Interrupt}

const createNewProcessGroup = 0x00000200

// childProcAttr: under the supervisor the command stays in its group; the
// foreground form gets its own so a console Ctrl-C does not hit it twice.
func childProcAttr(supervise bool) *syscall.SysProcAttr {
	if supervise {
		return nil
	}
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// forwardSignal stops the command: the foreground form kills its whole tree
// (taskkill /T /F) after checking, through a handle that pins the PID, that
// it is still the process started (creation time started); under the
// supervisor `host stop` already did, so kill only the direct child.
func forwardSignal(p *os.Process, started string, _ os.Signal, supervise bool) {
	if supervise || started == "" {
		p.Kill() //nolint:errcheck // it may have exited already
		return
	}
	state.KillHost(state.HostProc{PID: p.Pid, StartedAt: started}, syscall.SIGKILL) //nolint:errcheck // the tree may be gone
}

func exitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}
