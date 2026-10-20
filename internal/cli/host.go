// host.go implements `stackfork host NAME [--port N] -- COMMAND...`: the
// sanctioned way to run an app on the host (not in a container) for a
// stackfork-managed checkout. It gives the app a stable free port, the same
// environment `stackfork env --with-passwords` exports, and a proxy route for
// as long as it runs. The guard (internal/guard) allows it.
//
// `host start NAME -- COMMAND...` does the same detached: a supervisor (this
// binary re-run as `host --supervise`) owns the command, its log, its
// HostProcs entry and its route, and cleans them up when the command exits.
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
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/guard"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

var hostNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const hostUsage = "usage: host <name> [--port N] -- <command...> | host start <name> [--port N] -- <command...> | host stop|restart <name> | host logs <name> [-f]"

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
		case "stop", "restart", "logs":
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
		return hostStart(stderr, name, command, workDir, project, instanceDir, newStateBase, legacyStateBase, hostFlagArgs(*portFlag, *dirFlag, *portEnvFlag))
	}

	block, err := envBlock(project, instanceDir, newStateBase, legacyStateBase, true)
	if err != nil {
		return 0, err
	}

	os.Remove(lifecycle.HostStopFlag(instanceDir, name)) // a leftover from a stopper that died
	port, err := registerHostRoute(name, *portFlag, supervise, root, branch, project, instanceDir, newStateBase, legacyStateBase, stderr)
	if err != nil {
		return 0, err
	}
	defer unregisterHostRoute(name, root, branch, project, instanceDir, newStateBase, legacyStateBase, stderr)

	env := os.Environ()
	for _, line := range strings.Split(block, "\n") {
		if key, _, ok := strings.Cut(line, "="); ok && key != "" {
			env = append(env, line)
		}
	}
	p := strconv.Itoa(port)
	env = append(env, "PORT="+p, "NUXT_PORT="+p, "NITRO_PORT="+p, "HOST=127.0.0.1", guard.HostRunEnv+"=1")
	for _, e := range portEnv {
		env = append(env, e+"="+p)
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
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-sigCh:
			forwardSignal(cmd.Process, sig, supervise)
		case err := <-done:
			return exitCode(err)
		}
	}
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
func registerHostRoute(name string, requested int, supervise bool, root, branch, project, instanceDir, newStateBase, legacyStateBase string, stderr io.Writer) (int, error) {
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
	proc := state.HostProc{PID: os.Getpid(), StartedAt: started, Cmdline: os.Args, Port: port}
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
		delete(info.Routes, name)
		if info.HostProcs[name].PID == os.Getpid() { // never another supervisor's entry
			delete(info.HostProcs, name)
		}
		err = state.WritePrivate(filepath.Join(instanceDir, "manifest.json"), info)
	}
	lock.Release()
	if err != nil {
		fmt.Fprintf(stderr, "stackfork: warning: could not remove route %q: %v\n", name, err)
		return
	}
	lifecycle.ReloadProxyIfRunning(stderr, newStateBase, legacyStateBase)
}
