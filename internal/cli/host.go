// host.go implements `stackfork host NAME [--port N] -- COMMAND...`: the
// sanctioned way to run an app on the host (not in a container) for a
// stackfork-managed checkout. It gives the app a stable free port, the same
// environment `stackfork env --with-passwords` exports, and a proxy route for
// as long as it runs. The guard (internal/guard) allows it.
//
// `host start NAME -- COMMAND...` does the same detached: a supervisor (this
// binary re-run as `host --supervise`) owns the command, its log, its
// HostProcs entry and its route. When the command exits it drops the route;
// the entry stays, so the dashboard shows the app as exited with Start.
// `host stop|restart|logs NAME` manage it. See hostdetach.go.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/guard"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

var hostNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const hostUsage = "usage: host <name> [--port N] -- <command...> | host start <name> [--port N] [--wait-timeout D] -- <command...> | host stop|restart <name> | host logs <name> [-f] | host ls"

func cmdHost(stdout, stderr io.Writer, subArgs []string, root, branch, project, instanceDir, newStateBase, legacyStateBase string) (int, error) {
	if hostUnsupported != nil {
		return 0, hostUnsupported
	}
	supervise := false
	if len(subArgs) > 0 && subArgs[0] == "--supervise" { // internal: set by `host start`
		supervise, subArgs = true, subArgs[1:]
	}
	if len(subArgs) > 0 && !supervise {
		switch subArgs[0] {
		case "stop", "restart", "logs", "ls":
			return cmdHostManage(stdout, stderr, subArgs[0], subArgs[1:], root, branch, project, instanceDir, newStateBase, legacyStateBase)
		}
	}
	detach := false
	if len(subArgs) > 0 && subArgs[0] == "start" && !supervise {
		detach, subArgs = true, subArgs[1:]
	}
	// Everything after the first `--` is the command; it is never parsed
	// as stackfork flags.
	var command []string
	own := subArgs
	for i, a := range subArgs {
		if a == "--" {
			own, command = subArgs[:i], subArgs[i+1:]
			break
		}
	}
	fs := newFlagSet(stdout, "host")
	portFlag := fs.Int("port", 0, "")
	dirFlag := fs.String("dir", "", "")
	portEnvFlag := fs.String("port-env", "", "")
	waitFlag := fs.Duration("wait-timeout", 0, "")
	var name string
	if len(own) > 0 && !strings.HasPrefix(own[0], "-") {
		name, own = own[0], own[1:]
	}
	if err := fs.Parse(own); err != nil {
		return parseFailure(fs, err)
	}
	if name == "" && fs.NArg() > 0 {
		name = fs.Arg(0)
	}
	if name == "" || len(command) == 0 {
		return 2, errors.New(hostUsage)
	}
	if !hostNamePattern.MatchString(name) {
		return 2, fmt.Errorf("invalid name %q; use lowercase letters, digits and dashes", name)
	}
	if *portFlag < 0 || *portFlag > 65535 {
		return 2, fmt.Errorf("invalid --port %d", *portFlag)
	}
	workDir := hostWorkDir(root)
	if *dirFlag != "" {
		d, err := confinedDir(root, *dirFlag)
		if err != nil {
			return 2, err
		}
		workDir = d
	}
	var portEnv []string
	for _, e := range strings.Split(*portEnvFlag, ",") {
		if e = strings.TrimSpace(e); e != "" {
			portEnv = append(portEnv, e)
		}
	}
	if detach {
		return hostStart(stderr, name, command, workDir, project, instanceDir, newStateBase, legacyStateBase, hostFlagArgs(*portFlag, *dirFlag, *portEnvFlag), hostWaitTimeout(*waitFlag))
	}

	block, err := envBlock(project, instanceDir, newStateBase, legacyStateBase, true)
	if err != nil {
		return 0, err
	}

	os.Remove(lifecycle.HostStopFlag(instanceDir, name)) // a leftover from a stopper that died
	var restart []string
	if !supervise { // a foreground run: record how `host restart` and `up` start it again, detached
		dir := *dirFlag
		if rel, err := filepath.Rel(root, workDir); dir == "" && err == nil && rel != "." {
			dir = rel
		}
		restart = append([]string{"--state-dir", newStateBase, "--legacy-state-dir", legacyStateBase, "--instance", project, "host", "--supervise", name},
			hostFlagArgs(*portFlag, dir, *portEnvFlag)...)
		restart = append(append(restart, "--"), command...)
	}
	port, err := registerHostRoute(name, *portFlag, supervise, restart, root, branch, project, instanceDir, newStateBase, legacyStateBase, stderr)
	if err != nil {
		return 0, err
	}
	defer unregisterHostRoute(name, root, branch, project, instanceDir, newStateBase, legacyStateBase, stderr)

	p := strconv.Itoa(port)
	extra := []string{"PORT=" + p, "NUXT_PORT=" + p, "NITRO_PORT=" + p, "HOST=127.0.0.1"}
	for _, e := range portEnv {
		extra = append(extra, e+"="+p)
	}
	env := hostEnviron(block, extra...)

	if err := checkHostArgv(command); err != nil {
		return 0, err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = env
	cmd.Dir = workDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	cmd.SysProcAttr = childProcAttr(supervise)
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, hostSignals...)
	defer signal.Stop(sigCh)

	fmt.Fprintf(stderr, "stackfork: running %q on port %d for %s (routed while it runs)\n", name, port, project)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start %q: %w", command[0], err)
	}
	started, _ := state.ProcStart(cmd.Process.Pid) // unreaped yet, so the pid is still ours
	rec := state.HostRecord{Name: name, PID: cmd.Process.Pid, SupervisorPID: os.Getpid(), Argv: redactedArgv(instanceDir, command),
		Cwd: workDir, Port: port, StartedAt: started}
	if err := state.WriteHostRecord(instanceDir, rec); err != nil {
		fmt.Fprintf(stderr, "stackfork: warning: could not write the host record: %v\n", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-sigCh:
			forwardSignal(cmd.Process, started, sig, supervise)
		case err := <-done:
			code, cerr := exitCode(err)
			if cerr == nil {
				rec.ExitedAt, rec.ExitCode = time.Now().UTC().Format(time.RFC3339), &code
				state.WriteHostRecord(instanceDir, rec) //nolint:errcheck // best effort: the dashboard then shows plain "exited"
			}
			return code, cerr
		}
	}
}

// redactedArgv is argv with the instance's resolved secret values replaced
// (internal/redact), so the record never holds one.
func redactedArgv(instanceDir string, argv []string) []string {
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if m == nil {
		return append([]string{}, argv...)
	}
	secrets := lifecycle.SecretsForRedaction(m)
	out := make([]string, len(argv))
	for i, a := range argv {
		var b strings.Builder
		w := redact.NewWriter(&b, secrets)
		w.Write([]byte(a)) //nolint:errcheck // strings.Builder cannot fail
		w.Close()
		out[i] = b.String()
	}
	return out
}

// hostEnviron is the environment of a command stackfork runs on the host for
// an instance: ours, then each KEY=VALUE line of the instance's environment
// block (exec.Cmd keeps the last of a repeated name, so the block wins), then
// extra, then STACKFORK_HOST_RUN=1. Host steps and host exec steps share it.
func hostEnviron(block string, extra ...string) []string {
	env := os.Environ()
	for _, line := range strings.Split(block, "\n") {
		if key, _, ok := strings.Cut(line, "="); ok && key != "" {
			env = append(env, line)
		}
	}
	return append(append(env, extra...), guard.HostRunEnv+"=1")
}

// checkHostArgv applies checkBatchArgs on Windows, where cmd.exe would
// interpret the arguments of a .cmd or .bat target.
func checkHostArgv(argv []string) error {
	return checkHostArgvFor(runtime.GOOS, argv, exec.LookPath)
}

// checkHostArgvFor is checkHostArgv for an explicit OS and lookup, so every OS
// can test the Windows refusal.
func checkHostArgvFor(goos string, argv []string, lookPath func(string) (string, error)) error {
	if goos != "windows" {
		return nil
	}
	return checkBatchArgs(argv, lookPath)
}

// checkBatchArgs refuses, for a command that resolves to a .cmd or .bat file,
// arguments cmd.exe would interpret (BatBadBut): & | < > ^ % !.
func checkBatchArgs(argv []string, lookPath func(string) (string, error)) error {
	p, err := lookPath(argv[0])
	if err != nil {
		return nil // exec reports it
	}
	if ext := strings.ToLower(filepath.Ext(p)); ext != ".cmd" && ext != ".bat" {
		return nil
	}
	for _, a := range argv[1:] {
		if strings.ContainsAny(a, "&|<>^%!") {
			return fmt.Errorf("%s runs through cmd.exe, which would interpret %q; remove any of & | < > ^ %% ! from the arguments", argv[0], a)
		}
	}
	return nil
}

// confinedDir resolves dir under root, symlinks included, and refuses a result outside root.
func confinedDir(root, dir string) (string, error) {
	rootR, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	d, err := filepath.EvalSymlinks(filepath.Join(root, dir))
	if err != nil {
		return "", fmt.Errorf("--dir %q: %w", dir, err)
	}
	if rel, err := filepath.Rel(rootR, d); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("--dir %q is outside the checkout", dir)
	}
	return d, nil
}

// hostWorkDir keeps the caller's directory when it is inside the checkout
// (a monorepo package, say) and falls back to the checkout root otherwise
// (--instance from elsewhere).
func hostWorkDir(root string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(root, wd); err == nil && !strings.HasPrefix(rel, "..") {
			return wd
		}
	}
	return root
}

// registerHostRoute picks (or reuses) name's stable port, persists it, and
// registers the proxy route, all under the instance lock.
func registerHostRoute(name string, requested int, supervise bool, restart []string, root, branch, project, instanceDir, newStateBase, legacyStateBase string, stderr io.Writer) (int, error) {
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return 0, err
	}
	info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "host")
	if err != nil {
		lock.Release()
		return 0, err
	}
	if cur, ok := info.HostProcs[name]; ok && cur.PID != os.Getpid() && state.HostIdentity(cur) {
		lock.Release()
		return 0, fmt.Errorf("host process %s is already running (pid %d)", name, cur.PID)
	}
	port := requested
	switch {
	case port != 0:
	case info.HostPorts[name] != 0:
		port = info.HostPorts[name]
	default:
		taken, err := recordedPorts(newStateBase, legacyStateBase, project)
		if err != nil {
			lock.Release()
			return 0, err
		}
		for _, a := range info.Ports {
			taken[a.Host] = true
		}
		for _, p := range info.HostPorts {
			taken[p] = true
		}
		port, err = ports.Allocate(project, "host/"+name, 0, ports.RangeFromEnv(), func(p int) bool {
			return taken[p] || ports.Bound(p)
		})
		if err != nil {
			lock.Release()
			return 0, err
		}
	}
	if ports.Bound(port) {
		lock.Release()
		return 0, fmt.Errorf("port %d is already in use; free it or pass --port N", port)
	}
	if info.HostPorts == nil {
		info.HostPorts = map[string]int{}
	}
	if info.Routes == nil {
		info.Routes = map[string]int{}
	}
	info.HostPorts[name] = port
	info.Routes[name] = port
	// Claim the name in the same lock hold as the check above, so two
	// starts cannot both pass it.
	started, perr := state.ProcStart(os.Getpid())
	if perr != nil {
		lock.Release()
		return 0, perr
	}
	proc := state.HostProc{PID: os.Getpid(), StartedAt: started, Cmdline: os.Args, Port: port, Restart: restart}
	if supervise {
		proc.Log = filepath.Join(instanceDir, "host-"+name+".log")
	}
	if info.HostProcs == nil {
		info.HostProcs = map[string]state.HostProc{}
	}
	info.HostProcs[name] = proc
	err = state.WritePrivate(filepath.Join(instanceDir, "manifest.json"), info)
	lock.Release()
	if err != nil {
		return 0, err
	}
	lifecycle.ReloadProxyIfRunning(stderr, newStateBase, legacyStateBase)
	return port, nil
}

// unregisterHostRoute removes the route when the command ends. HostPorts
// keeps the port for next time.
func unregisterHostRoute(name, root, branch, project, instanceDir, newStateBase, legacyStateBase string, stderr io.Writer) {
	if lifecycle.HostStopperActive(instanceDir, name) {
		return // a live stopper is cleaning up for us (and may hold the lock)
	}
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		fmt.Fprintf(stderr, "stackfork: warning: could not remove route %q: %v\n", name, err)
		return
	}
	info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "host")
	if err == nil {
		delete(info.Routes, name) // the HostProcs entry stays: the row shows exited, Start respawns it
		err = state.WritePrivate(filepath.Join(instanceDir, "manifest.json"), info)
	}
	lock.Release()
	if err != nil {
		fmt.Fprintf(stderr, "stackfork: warning: could not remove route %q: %v\n", name, err)
		return
	}
	lifecycle.ReloadProxyIfRunning(stderr, newStateBase, legacyStateBase)
}
