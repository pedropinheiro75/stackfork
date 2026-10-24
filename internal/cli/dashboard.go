// dashboard.go wires internal/dashboard into the CLI: `stackfork dashboard
// [--port N] [--open]` (task 2.3, design.md D2). A stable port comes from
// internal/ports keyed "stackfork/dashboard", persisted under
// <state>/dashboard/ so restarting the command reuses the same URL, the way
// prepare's stable service ports do (design.md D7 layer 2).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func dashboardStateDir(stateBase string) string { return filepath.Join(stateBase, "dashboard") }

// stableDashboardPort returns the persisted port if it's still free, else
// allocates a new one from the shared range (deterministic first choice,
// design.md D7 layer 2's allocator reused here for task 2.3) and persists
// it.
func stableDashboardPort(stateBase string) (int, error) {
	return stablePort(stateBase, "port", "dashboard", 0)
}

// stablePort is stableDashboardPort for any file under <state>/dashboard and
// any allocator key: `--supervised` keeps its own ("app-port",
// "dashboard-app") so the CLI dashboard's URL never moves (add-macos-app D4).
//
// reserved is a port that counts as taken (0: none): the supervised app keeps
// off the port the CLI dashboard recorded, even while that one is stopped.
func stablePort(stateBase, fileName, key string, reserved int) (int, error) {
	file := filepath.Join(dashboardStateDir(stateBase), fileName)
	if data, err := os.ReadFile(file); err == nil {
		if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p != reserved && !ports.Bound(p) {
			return p, nil
		}
	}
	rng := ports.RangeFromEnv()
	p, err := ports.Allocate("stackfork", key, 0, rng, func(p int) bool { return p == reserved || ports.Bound(p) })
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dashboardStateDir(stateBase), 0o700); err != nil {
		return 0, err
	}
	if err := os.WriteFile(file, []byte(strconv.Itoa(p)+"\n"), 0o600); err != nil {
		return 0, err
	}
	return p, nil
}

// dashboardStdin is what `--supervised` watches for end of file; a var so a
// test can hold the "parent's" pipe.
var dashboardStdin io.Reader = os.Stdin

// dashboardStartEnv is StartEnv for the dashboard's Start button; a var so a
// test can block it.
var dashboardStartEnv func(stdout, stderr io.Writer, o StartOptions) error

func init() { dashboardStartEnv = StartEnv } // not a var initializer: StartEnv reaches cmdDashboard

// readyLine is the one JSON line `--supervised` prints once it listens
// (add-macos-app D5). Schema 1 has exactly these keys.
type readyLine struct {
	Event   string `json:"event"`
	Schema  int    `json:"schema"`
	URL     string `json:"url"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

func cmdDashboard(ctx context.Context, stdout, stderr io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	fs := newFlagSet(stdout, "dashboard")
	portFlag := fs.Int("port", 0, "")
	openFlag := fs.Bool("open", false, "")
	supervised := fs.Bool("supervised", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	portSet := false
	fs.Visit(func(f *flag.Flag) { portSet = portSet || f.Name == "port" })
	if *supervised && (portSet || *openFlag) {
		// (2, nil): Run turns an error into exit 1, and this one is a usage error.
		fmt.Fprintln(stderr, "stackfork: --supervised chooses its own port and opens no browser; drop --port and --open")
		return 2, nil
	}

	// Bind first: nothing is announced, registered or warmed up unless the
	// port is really ours. The server needs the final port for its Host
	// allowlist, so this comes before dashboard.New.
	var ln net.Listener
	var err error
	port := *portFlag
	if *supervised {
		// Its own stable port; when the range is full or the bind races,
		// the kernel chooses and nothing is recorded (add-macos-app D4).
		cliPort := 0
		if data, rerr := os.ReadFile(filepath.Join(dashboardStateDir(newStateBase), "port")); rerr == nil {
			cliPort, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if p, perr := stablePort(newStateBase, "app-port", "dashboard-app", cliPort); perr == nil {
			ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		}
		if ln == nil {
			if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
				return 0, err
			}
		}
		port = ln.Addr().(*net.TCPAddr).Port
	} else {
		if port == 0 {
			if port, err = stableDashboardPort(newStateBase); err != nil {
				return 0, err
			}
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		if ln, err = net.Listen("tcp", addr); err != nil {
			return 0, bindError(addr, err)
		}
	}
	defer ln.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	srv, err := dashboard.New(newStateBase, legacyStateBase, Version, port)
	if err != nil {
		return 0, err
	}
	// The repo browser's Start button runs the same code as `start-env`.
	profiles, err := resolveProfileDirs(nil)
	if err != nil {
		return 0, err
	}
	srv.ProfileDirs = profiles
	srv.StartEnv = func(out, errOut io.Writer, checkout, trigger string, obs func(runs.Event)) error {
		return dashboardStartEnv(out, errOut, StartOptions{
			Checkout: checkout, NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, ProfileDirs: profiles, Trigger: trigger, Observer: obs,
		})
	}
	srv.DerivePlan = func(checkout string, insts []inventory.Instance) (lifecycle.DerivedPlan, error) {
		return DerivePlan(DeriveOptions{Checkout: checkout, NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, ProfileDirs: profiles, Instances: insts})
	}
	srv.ResetEnv = func(out, errOut io.Writer, checkout string, current, fromMain bool, fingerprint string, obs func(runs.Event)) error {
		return StartEnv(out, errOut, StartOptions{
			Checkout: checkout, NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, ProfileDirs: profiles,
			Trigger: dashboard.TriggerDashboard, Observer: obs, Reset: true, Current: current, FromMain: fromMain, Fingerprint: fingerprint,
		})
	}
	srv.ResetPreview = func(checkout string, fromMain bool) (string, bool, string, error) {
		p, err := PreviewReset(checkout, newStateBase, legacyStateBase, fromMain)
		return p.Describe(), p.Blocked, p.Fingerprint, err
	}
	srv.LiveCleanup = dashboardCleanup(newStateBase, legacyStateBase)
	var liveLoop atomic.Pointer[live.Loop]
	srv.Live = dashboardLive(newStateBase, legacyStateBase, &liveLoop)
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}

	// Register with the shared proxy, if it's running (design.md D2/task
	// 2.4): stackfork.localhost joins every instance's route while the
	// dashboard runs, and comes back out on shutdown. Best effort, like
	// every other proxy reload - a problem here never blocks the dashboard
	// itself from serving on its own port.
	// It runs in the background, after the address is announced: Docker can
	// take seconds to answer, and the page must not wait for it.
	var registeredWithProxy atomic.Bool
	registered := make(chan struct{})
	startRegistration := func() {
		defer close(registered)
		if registerDashboardWithProxy(newStateBase, legacyStateBase, port, stderr) {
			registeredWithProxy.Store(true)
		}
	}
	unregister := func() {
		if *supervised {
			return // never registered; the CLI dashboard's route stays
		}
		<-registered // never unregister while the registration is still running
		if !registeredWithProxy.Load() {
			return
		}
		mode, err := proxy.Mode()
		if err != nil {
			return
		}
		routes, err := lifecycle.GatherRoutes(newStateBase, legacyStateBase, mode)
		if err != nil {
			return
		}
		if err := dashboard.UnregisterFromProxy(newStateBase, routes); err != nil {
			fmt.Fprintf(stderr, "warning: proxy unregister failed: %s\n", err)
		}
	}

	// Armed before anything is announced or started: a signal that arrives
	// the instant a caller sees the "listening on" line must still be
	// caught here, not fall through to the OS default (immediate
	// termination, skipping the proxy unregister below).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()

	// stdinGone is end of file on standard input: the parent is gone, which
	// counts as a first SIGTERM (add-macos-app D4).
	stdinGone := make(chan struct{})
	if *supervised {
		line, _ := json.Marshal(readyLine{Event: "ready", Schema: 1, URL: "http://" + addr + "/", Port: port, Token: srv.Token, PID: os.Getpid(), Version: Version})
		fmt.Fprintf(stdout, "%s\n", line)
		go func() { io.Copy(io.Discard, dashboardStdin); close(stdinGone) }() //nolint:errcheck // a read error is the same as EOF
	} else {
		fmt.Fprintf(stdout, "stackfork dashboard listening on http://%s\n", addr)
		go startRegistration()
	}
	go srv.Warm() // first collection and repo scan, off the request path
	loop, stopLoop, loopDone := startLiveLoop(newStateBase, legacyStateBase, profiles, srv)
	liveLoop.Store(loop)
	defer func() { stopLoop(); <-loopDone }()
	if *openFlag {
		openBrowser("http://" + addr)
	}

	select {
	case <-ctx.Done():
	case <-sigCh:
	case <-stdinGone:
	case err := <-errCh:
		unregister()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return 0, err
		}
		return 0, nil
	}

	unregister()
	// Shutdown closes the listener at once (a new dashboard can take the
	// port) but lets a running start/up/run finish: killing it leaves a
	// half-started stack and a "network error" on the page. A second
	// signal forces it.
	wait := 5 * time.Second
	stopLoop() // a live run in progress finishes first
	if n := srv.Running() + loop.Running(); n > 0 {
		fmt.Fprintf(stdout, "waiting for %d running action(s) to finish (signal again to stop now)\n", n)
		wait = 15 * time.Minute
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-shutdownCtx.Done():
		}
	}()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		return 0, err
	}
	select { // the live run in progress, if any
	case <-loopDone:
	case <-shutdownCtx.Done():
	}
	return 0, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Run() //nolint:errcheck // best effort; the printed URL is the fallback
}

// registerDashboardWithProxy joins the shared proxy, if it is running
// (design.md D2/task 2.4): stackfork.localhost routes to the dashboard while
// it runs. Best effort - a problem here never blocks the dashboard. A var so
// a test can make it slow.
var registerDashboardWithProxy = func(newStateBase, legacyStateBase string, port int, stderr io.Writer) bool {
	if running, _ := proxy.IsRunning(proxy.ContainerName()); !running {
		return false
	}
	mode, err := proxy.Mode()
	if err != nil {
		return false
	}
	routes, err := lifecycle.GatherRoutes(newStateBase, legacyStateBase, mode)
	if err != nil {
		return false
	}
	if err := dashboard.RegisterWithProxy(newStateBase, routes, port); err != nil {
		fmt.Fprintf(stderr, "warning: proxy registration skipped: %s\n", err)
		return false
	}
	return true
}

// bindError explains a failed bind, and says so when the port is held by
// another stackfork dashboard (its API answers) rather than something else.
func bindError(addr string, err error) error {
	client := &http.Client{Timeout: 2 * time.Second} // a busy machine answers slowly; only paid when the port is taken
	// The marker header is on every answer, so a cheap page is enough: no
	// inventory (and no docker call) that a busy machine answers too slowly.
	if resp, gerr := client.Get("http://" + addr + "/api/disk"); gerr == nil {
		resp.Body.Close()
		if resp.Header.Get("X-Stackfork") == "dashboard" {
			return fmt.Errorf("a stackfork dashboard is already running at http://%s; open it, stop it first, or pass another --port", addr)
		}
	}
	return fmt.Errorf("cannot listen on %s (%v); the port is in use, so another process or dashboard may be running - pass another --port", addr, err)
}
