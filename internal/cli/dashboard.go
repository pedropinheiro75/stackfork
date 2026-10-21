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
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func dashboardStateDir(stateBase string) string { return filepath.Join(stateBase, "dashboard") }
func dashboardPortFile(stateBase string) string {
	return filepath.Join(dashboardStateDir(stateBase), "port")
}

// stableDashboardPort returns the persisted port if it's still free, else
// allocates a new one from the shared range (deterministic first choice,
// design.md D7 layer 2's allocator reused here for task 2.3) and persists
// it.
func stableDashboardPort(stateBase string) (int, error) {
	if data, err := os.ReadFile(dashboardPortFile(stateBase)); err == nil {
		if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && !ports.Bound(p) {
			return p, nil
		}
	}
	rng := ports.RangeFromEnv()
	p, err := ports.Allocate("stackfork", "dashboard", 0, rng, ports.Bound)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dashboardStateDir(stateBase), 0o700); err != nil {
		return 0, err
	}
	if err := os.WriteFile(dashboardPortFile(stateBase), []byte(strconv.Itoa(p)+"\n"), 0o600); err != nil {
		return 0, err
	}
	return p, nil
}

func cmdDashboard(ctx context.Context, stdout, stderr io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	fs := newFlagSet(stdout, "dashboard")
	portFlag := fs.Int("port", 0, "")
	openFlag := fs.Bool("open", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}

	port := *portFlag
	if port == 0 {
		p, err := stableDashboardPort(newStateBase)
		if err != nil {
			return 0, err
		}
		port = p
	}

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
		return StartEnv(out, errOut, StartOptions{
			Checkout: checkout, NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, ProfileDirs: profiles, Trigger: trigger, Observer: obs,
		})
	}
	srv.ResetEnv = func(out, errOut io.Writer, checkout string, current bool, obs func(runs.Event)) error {
		return StartEnv(out, errOut, StartOptions{
			Checkout: checkout, NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, ProfileDirs: profiles,
			Trigger: dashboard.TriggerDashboard, Observer: obs, Reset: true, Current: current,
		})
	}
	srv.ResetPreview = func(checkout string) (string, bool, error) {
		p, err := PreviewReset(checkout, newStateBase, legacyStateBase, false)
		return p.Describe(), p.Blocked, err
	}
	srv.LiveCleanup = dashboardCleanup(newStateBase, legacyStateBase)
	var liveLoop atomic.Pointer[live.Loop]
	srv.Live = dashboardLive(newStateBase, legacyStateBase, &liveLoop)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}

	// Bind first: nothing is announced, registered or warmed up unless the
	// port is really ours.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, bindError(addr, err)
	}
	defer ln.Close()

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

	fmt.Fprintf(stdout, "stackfork dashboard listening on http://%s\n", addr)
	go startRegistration()
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
	client := &http.Client{Timeout: 800 * time.Millisecond}
	if resp, gerr := client.Get("http://" + addr + "/api/instances"); gerr == nil {
		defer resp.Body.Close()
		var probe struct {
			Instances json.RawMessage `json:"instances"`
			Docker    json.RawMessage `json:"docker"`
		}
		if resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&probe) == nil && probe.Docker != nil {
			return fmt.Errorf("a stackfork dashboard is already running at http://%s; open it, stop it first, or pass another --port", addr)
		}
	}
	return fmt.Errorf("cannot listen on %s (%v); the port is in use, so another process or dashboard may be running - pass another --port", addr, err)
}
