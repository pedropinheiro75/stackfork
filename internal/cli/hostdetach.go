package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// hostExecutable is the binary the supervisor is re-run from (a test swaps it).
var hostExecutable = os.Executable

func init() {
	lifecycle.HostRestart = func(stderr io.Writer, root, instanceDir, name, newBase, legacyBase string) error {
		m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
		if err != nil || m == nil {
			return fmt.Errorf("no prepared stack for %s", instanceDir)
		}
		proc, ok := m.HostProcs[name]
		if !ok {
			return fmt.Errorf("no host process %s", name)
		}
		if len(proc.Cmdline) < 3 || !contains(proc.Cmdline, "--supervise") {
			return fmt.Errorf("%s was not started with `host start`; start it again", name)
		}
		if err := lifecycle.StopHostLocked(stderr, instanceDir, name, newBase, legacyBase); err != nil {
			return err
		}
		_, err = spawnSupervisor(stderr, name, proc.Cmdline[1:], hostWorkDir(root), instanceDir)
		return err
	}
	lifecycle.HostStart = func(out, errOut io.Writer, base []string, root, instanceDir string, step knowledge.Step) error {
		argv, err := knowledge.SplitArgv(step.Cmd)
		if err != nil || len(argv) == 0 {
			return fmt.Errorf("host step %s: cannot read its command %q", step.ID, step.Cmd)
		}
		flags := hostFlagArgs(step.Port, step.Dir, strings.Join(step.PortEnv, ","))
		// A start is idempotent: the same app already running is success.
		if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
			if proc, ok := m.HostProcs[step.Name]; ok && state.HostIdentity(proc) {
				want := append(append(append([]string{"--supervise", step.Name}, flags...), "--"), argv...)
				i := 0
				for i < len(proc.Cmdline) && proc.Cmdline[i] != "--supervise" {
					i++
				}
				if strings.Join(proc.Cmdline[i:], "\x00") != strings.Join(want, "\x00") {
					return fmt.Errorf("host app %s is already running (pid %d) with a different command; run `stackfork host restart %s` or stop it", step.Name, proc.PID, step.Name)
				}
				fmt.Fprintf(out, "host app %s already running (pid %d)\n", step.Name, proc.PID)
				return nil
			}
		}
		args := append([]string{}, base...)
		if len(args) == 0 {
			args = []string{"--root", root}
		}
		args = append(append(args, "host", "start", step.Name), flags...)
		code, err := run(append(append(args, "--"), argv...), out, errOut)
		if err == nil && code != 0 {
			err = fmt.Errorf("host start %s exited with status %d", step.Name, code)
		}
		return err
	}
}

func hostFlagArgs(port int, dir, portEnv string) []string {
	var a []string
	if port != 0 {
		a = append(a, "--port", strconv.Itoa(port))
	}
	if dir != "" {
		a = append(a, "--dir", dir)
	}
	if portEnv != "" {
		a = append(a, "--port-env", portEnv)
	}
	return a
}

// hostStart spawns the detached supervisor for a new host process.
func hostStart(stderr io.Writer, name string, command []string, workDir, project, instanceDir, newBase, legacyBase string, flagArgs []string) (int, error) {
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
		if proc, ok := m.HostProcs[name]; ok && state.HostIdentity(proc) {
			return 0, fmt.Errorf("host process %s is already running (pid %d); use `host restart %s`", name, proc.PID, name)
		}
	}
	args := []string{"--state-dir", newBase, "--legacy-state-dir", legacyBase, "--instance", project, "host", "--supervise", name}
	args = append(append(args, flagArgs...), "--")
	return spawnSupervisor(stderr, name, append(args, command...), workDir, instanceDir)
}

// spawnSupervisor runs `stackfork ARGS` detached, log to host-NAME.log, and
// returns once the supervisor recorded its HostProcs entry.
func spawnSupervisor(stderr io.Writer, name string, args []string, workDir, instanceDir string) (int, error) {
	exe, err := hostExecutable()
	if err != nil {
		return 0, err
	}
	logPath := filepath.Join(instanceDir, "host-"+name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, args...)
	cmd.Dir = workDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, logFile, logFile // nil stdin is /dev/null
	cmd.SysProcAttr = state.DetachAttr()
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start the supervisor: %w", err)
	}
	pid := cmd.Process.Pid
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }() //nolint:errcheck // reaps it; the manifest tells the story
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-exited:
			tail, _ := os.ReadFile(logPath)
			return 0, fmt.Errorf("host process %s exited at start:\n%s", name, strings.TrimSpace(string(tail)))
		case <-deadline:
			select {
			case <-exited: // it died just now: report that, signal nothing
				tail, _ := os.ReadFile(logPath)
				return 0, fmt.Errorf("host process %s exited at start:\n%s", name, strings.TrimSpace(string(tail)))
			default:
			}
			if state.KillGroup(pid, syscall.SIGKILL) != nil { // unreaped, so the pid is still ours; the group (Windows: tree) dies with it
				cmd.Process.Kill() //nolint:errcheck // not a group leader (yet): at least the process
			}
			tail, _ := os.ReadFile(logPath)
			return 0, fmt.Errorf("host process %s did not report in 10s; see %s\n%s", name, logPath, strings.TrimSpace(string(tail)))
		case <-time.After(50 * time.Millisecond):
		}
		if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
			if p, ok := m.HostProcs[name]; ok && p.PID == pid {
				select { // a supervisor that fails to start its command dies right after claiming the name
				case <-exited:
					tail, _ := os.ReadFile(logPath)
					return 0, fmt.Errorf("host process %s exited at start:\n%s", name, strings.TrimSpace(string(tail)))
				case <-time.After(300 * time.Millisecond):
				}
				fmt.Fprintf(stderr, "stackfork: started %s (pid %d, port %d); logs: stackfork host logs %s\n", name, pid, p.Port, name)
				return 0, nil
			}
		}
	}
}

// cmdHostManage runs host stop|restart|logs NAME.
func cmdHostManage(stdout, stderr io.Writer, sub string, args []string, root, branch, project, instanceDir, newBase, legacyBase string) (int, error) {
	follow := false
	var name string
	for _, a := range args {
		switch {
		case a == "-f" && sub == "logs":
			follow = true
		case strings.HasPrefix(a, "-"):
			return 2, fmt.Errorf("unknown flag %s; %s", a, hostUsage)
		case name == "":
			name = a
		default:
			return 2, errors.New(hostUsage)
		}
	}
	if name == "" {
		return 2, errors.New(hostUsage)
	}
	manifestPath := filepath.Join(instanceDir, "manifest.json")
	m, err := state.ReadManifest(manifestPath)
	if err != nil {
		return 0, err
	}
	if m == nil {
		return 0, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	proc, ok := m.HostProcs[name]
	if sub == "logs" {
		return hostLogs(stdout, m, instanceDir, name, follow)
	}
	if !ok {
		return 0, fmt.Errorf("no host process %s", name)
	}
	if sub == "restart" && (len(proc.Cmdline) < 3 || !contains(proc.Cmdline, "--supervise")) {
		return 0, fmt.Errorf("%s was not started with `host start`; start it again", name)
	}
	err = lifecycle.StopHostLocked(stderr, instanceDir, name, newBase, legacyBase)
	if err != nil {
		return 0, err
	}
	if sub == "stop" {
		fmt.Fprintf(stdout, "stopped %s\n", name)
		return 0, nil
	}
	return spawnSupervisor(stderr, name, proc.Cmdline[1:], hostWorkDir(root), instanceDir)
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// hostLogs prints host-NAME.log (secrets redacted), and with -f keeps following it.
func hostLogs(stdout io.Writer, m *state.Manifest, instanceDir, name string, follow bool) (int, error) {
	f, err := os.Open(filepath.Join(instanceDir, "host-"+name+".log"))
	if err != nil {
		return 0, fmt.Errorf("no log for host process %s", name)
	}
	defer f.Close()
	w := lifecycle.NewRedactedWriter(stdout, m)
	defer w.Close()
	for {
		if _, err := io.Copy(w, f); err != nil {
			return 0, err
		}
		if !follow {
			return 0, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
}
