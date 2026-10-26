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

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/ports"
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
		if proc.RestartArgs() == nil {
			return fmt.Errorf("%s was not started with `host start`; start it again", name)
		}
		if err := lifecycle.StopHostLocked(stderr, instanceDir, name, newBase, legacyBase); err != nil {
			return err
		}
		_, err = spawnSupervisor(stderr, name, proc.RestartArgs(), hostWorkDir(root), instanceDir, restartWait(proc.RestartArgs()), false)
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
		if step.Port != 0 || len(step.PortEnv) > 0 || knowledge.UsesSelfPort(argv) {
			args = append(args, "--require-listen")
		}
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
func hostStart(stderr io.Writer, name string, command []string, workDir, project, instanceDir, newBase, legacyBase string, flagArgs []string, wait time.Duration, require bool) (int, error) {
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
		if proc, ok := m.HostProcs[name]; ok && state.HostIdentity(proc) {
			return 0, fmt.Errorf("host process %s is already running (pid %d); use `host restart %s`", name, proc.PID, name)
		}
	}
	args := []string{"--state-dir", newBase, "--legacy-state-dir", legacyBase, "--instance", project, "host", "--supervise", name}
	args = append(append(args, flagArgs...), "--")
	return spawnSupervisor(stderr, name, append(args, command...), workDir, instanceDir, wait, require)
}

// hostClaimWait is how long a start with no port wait gives the supervisor to
// record the app. A loaded machine took more than 10 s once (a flaky test).
var hostClaimWait = 60 * time.Second

// hostWaitDefault is how long `host start` waits for the app to listen.
const hostWaitDefault = 90 * time.Second

// hostWaitTimeout is the wait for the port: flag when set (0 included: no
// wait), else STACKFORK_HOST_WAIT_TIMEOUT (a Go duration), else 90 s.
func hostWaitTimeout(flag time.Duration, set bool) time.Duration {
	if set {
		return flag
	}
	if v := os.Getenv("STACKFORK_HOST_WAIT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return hostWaitDefault
}

// waitFromArgs reads the --wait-timeout a supervisor command line remembers.
func waitFromArgs(args []string) (time.Duration, bool) {
	for i, a := range args {
		if a == "--" {
			break
		}
		if a == "--wait-timeout" && i+1 < len(args) {
			if d, err := time.ParseDuration(args[i+1]); err == nil && d >= 0 {
				return d, true
			}
		}
	}
	return 0, false
}

// restartWait is the wait a restart or up uses: the one the app was started with.
func restartWait(args []string) time.Duration {
	d, set := waitFromArgs(args)
	return hostWaitTimeout(d, set)
}

// hostLogTail is the last n lines of host-NAME.log, secrets redacted.
func hostLogTail(instanceDir, name string, n int) string {
	b, err := os.ReadFile(filepath.Join(instanceDir, "host-"+name+".log"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	text := strings.Join(lines, "\n") + "\n"
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
		var out strings.Builder
		w := lifecycle.NewRedactedWriter(&out, instanceDir, m)
		w.Write([]byte(text)) //nolint:errcheck // strings.Builder cannot fail
		w.Close()
		return out.String()
	}
	return text
}

// spawnSupervisor runs `stackfork ARGS` detached, log to host-NAME.log, and
// returns once the supervisor recorded its HostProcs entry and, when wait is
// not zero, the app listens on its port. wait is the deadline of the whole
// start; with zero the entry alone has hostClaimWait.
func spawnSupervisor(stderr io.Writer, name string, args []string, workDir, instanceDir string, wait time.Duration, require bool) (int, error) {
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
	failed := func() error {                  // the log is redacted: this text reaches run errors and action responses
		return fmt.Errorf("host process %s exited at start:\n%s", name, strings.TrimSpace(hostLogTail(instanceDir, name, 15)))
	}
	claimWait := wait
	if claimWait == 0 {
		claimWait = hostClaimWait
	}
	deadline := time.After(claimWait)
	for {
		select {
		case <-exited:
			return 0, failed()
		case <-deadline:
			select {
			case <-exited: // it died just now: report that, signal nothing
				return 0, failed()
			default:
			}
			// It never recorded itself, so it holds no route. TERM first anyway, KILL after 5 s.
			state.KillGroup(pid, syscall.SIGTERM) //nolint:errcheck // unreaped, so the pid is still ours
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				if state.KillGroup(pid, syscall.SIGKILL) != nil {
					cmd.Process.Kill() //nolint:errcheck // not a group leader (yet): at least the process
				}
			}
			return 0, fmt.Errorf("host process %s did not report in %v; see %s\n%s", name, claimWait, logPath, strings.TrimSpace(hostLogTail(instanceDir, name, 15)))
		case <-time.After(50 * time.Millisecond):
		}
		if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
			if p, ok := m.HostProcs[name]; ok && p.PID == pid {
				select { // a supervisor that fails to start its command dies right after claiming the name
				case <-exited:
					return 0, failed()
				case <-time.After(300 * time.Millisecond):
				}
				for wait > 0 {
					if ports.Listening(p.Port) {
						// cheap ownership check: the supervisor is alive now and still is half a second on
						select {
						case <-exited:
							return 0, failed()
						case <-time.After(500 * time.Millisecond):
						}
						break
					}
					select {
					case <-exited:
						return 0, failed()
					case <-deadline:
						if require { // the plan declared a port: an app that never listens did not start
							return 0, fmt.Errorf("host process %s is running but not listening on %d after %v; it is still running (stackfork host stop %s). Last log lines of %s:\n%s",
								name, p.Port, wait, name, logPath, strings.TrimSpace(hostLogTail(instanceDir, name, 15)))
						}
						// A worker may never listen, or not use PORT: leave it running, as `starting`.
						fmt.Fprintf(stderr, "stackfork: warning: %s is running but not listening on %d after %v; it may not use PORT. Last log lines of %s:\n%s\n",
							name, p.Port, wait, logPath, strings.TrimSpace(hostLogTail(instanceDir, name, 15)))
						return 0, nil
					case <-time.After(100 * time.Millisecond):
					}
				}
				fmt.Fprintf(stderr, "stackfork: started %s (pid %d, port %d); logs: stackfork host logs %s\n", name, pid, p.Port, name)
				return 0, nil
			}
		}
	}
}

// cmdHostManage runs host stop|restart|logs NAME.
func cmdHostManage(stdout, stderr io.Writer, sub string, args []string, root, branch, project, instanceDir, newBase, legacyBase string) (int, error) {
	if sub == "ls" {
		return hostLs(stdout, args, instanceDir)
	}
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
	if sub == "restart" && proc.RestartArgs() == nil {
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
	return spawnSupervisor(stderr, name, proc.RestartArgs(), hostWorkDir(root), instanceDir, restartWait(proc.RestartArgs()), false)
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
	w := lifecycle.NewRedactedWriter(stdout, instanceDir, m)
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

// hostLs prints each recorded host app: state, port, and the dashboard's
// summary (pid, memory, CPU, uptime, or how it exited).
func hostLs(stdout io.Writer, args []string, instanceDir string) (int, error) {
	if len(args) > 0 {
		return 2, errors.New(hostUsage)
	}
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return 0, err
	}
	if m == nil {
		return 0, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	apps := inventory.HostApps(m, instanceDir)
	if len(apps) == 0 {
		fmt.Fprintln(stdout, "no host apps")
		return 0, nil
	}
	fmt.Fprintf(stdout, "%-16s %-8s %-6s %s\n", "NAME", "STATE", "PORT", "DETAIL")
	for _, a := range apps {
		fmt.Fprintf(stdout, "%-16s %-8s %-6d %s\n", a.Label, a.State, m.HostProcs[a.Label].Port, a.Meta)
	}
	return 0, nil
}
