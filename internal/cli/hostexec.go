package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// hostExecWaitDelay is how long a host exec step waits for its output pipes
// after the command exited: a grandchild that keeps them open must not hold
// the run, and the instance lock, for as long as it lives.
var hostExecWaitDelay = 5 * time.Second

func init() {
	lifecycle.HostExec = runHostExec
}

// runHostExec runs an exec step with on: host: an argument vector without a
// shell, in the checkout or its confined dir, with the instance's environment
// block and stdin closed. Stdout and stderr both go to w, the step writer
// that redacts. The exit status is the step's; a command that cannot start is
// an error. It stays in stackfork's process group, so Ctrl-C reaches it.
func runHostExec(w io.Writer, project, root, instanceDir, newBase, legacyBase string, step knowledge.Step) (int, error) {
	argv, err := knowledge.SplitArgv(step.Cmd)
	if err != nil || len(argv) == 0 {
		return 0, fmt.Errorf("exec step %s on host: cannot read its command", step.ID)
	}
	dir := root
	if step.Dir != "" {
		if dir, err = confinedDir(root, step.Dir); err != nil {
			return 0, err
		}
	}
	if argv, err = expandHostCommand(argv, "", 0, false, root, project, instanceDir, newBase, legacyBase); err != nil {
		return 0, fmt.Errorf("exec step %s on host: %w", step.ID, err)
	}
	if err := checkHostArgv(argv); err != nil {
		return 0, err
	}
	block, err := envBlock(project, instanceDir, newBase, legacyBase, true)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Dir = hostEnviron(block), dir
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = w, w, hostExecWaitDelay
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start %q: %w", argv[0], err)
	}
	err = cmd.Wait()
	if errors.Is(err, exec.ErrWaitDelay) { // it exited with status 0; something it started still holds the output
		fmt.Fprintf(w, "note: stopped waiting for output of a process %s started\n", argv[0])
		err = nil
	}
	return exitCode(err)
}
