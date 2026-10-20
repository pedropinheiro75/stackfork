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

// hostStartBase is the global flags of the start-env in progress (state dirs,
// --root), read by the default lifecycle.HostStart below.
var hostStartBase []string

func init() {
	lifecycle.HostStart = func(out, errOut io.Writer, root, instanceDir string, step knowledge.Step) error {
		argv, err := knowledge.SplitArgv(step.Cmd)
		if err != nil || len(argv) == 0 {
			return fmt.Errorf("host step %s: cannot read its command %q", step.ID, step.Cmd)
		}
		args := append([]string{}, hostStartBase...)
		if len(args) == 0 {
			args = []string{"--root", root}
		}
		args = append(append(args, "host", "start", step.Name), hostFlagArgs(step.Port, step.Dir, strings.Join(step.PortEnv, ","))...)
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

// recordHostProc writes this process's HostProcs entry, under the instance lock.
func recordHostProc(instanceDir, name string, port int, supervise bool, stderr io.Writer) error {
	started, err := state.ProcStart(os.Getpid())
	if err != nil {
		return err
	}
	proc := state.HostProc{PID: os.Getpid(), StartedAt: started, Cmdline: os.Args, Port: port}
	if supervise {
		proc.Log = filepath.Join(instanceDir, "host-"+name+".log")
	}
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil || m == nil {
		return fmt.Errorf("manifest: %v", err)
	}
	if m.HostProcs == nil {
		m.HostProcs = map[string]state.HostProc{}
	}
	m.HostProcs[name] = proc
	return state.WritePrivate(filepath.Join(instanceDir, "manifest.json"), m)
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
			state.KillGroup(pid, syscall.SIGKILL) //nolint:errcheck // our own new session leader
			tail, _ := os.ReadFile(logPath)
			return 0, fmt.Errorf("host process %s did not report in 10s; see %s\n%s", name, logPath, strings.TrimSpace(string(tail)))
		case <-time.After(50 * time.Millisecond):
		}
		if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
			if p, ok := m.HostProcs[name]; ok && p.PID == pid {
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
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return 0, err
	}
	err = lifecycle.StopHost(stderr, instanceDir, name, lifecycle.HostStopTimeout())
	lock.Release()
	lifecycle.ReloadProxyIfRunning(stderr, newBase, legacyBase)
	if err != nil {
		return 0, err
	}
	if sub == "stop" {
		fmt.Fprintf(stdout, "stopped %s\n", name)
		return 0, nil
	}
	if len(proc.Cmdline) < 3 || !contains(proc.Cmdline, "--supervise") {
		return 0, fmt.Errorf("%s was not started with `host start`; start it again", name)
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
