// dashboard.go wires internal/dashboard into the CLI: `stackfork dashboard
// [--port N] [--open]` (task 2.3, design.md D2). A stable port comes from
// internal/ports keyed "stackfork/dashboard", persisted under
// <state>/dashboard/ so restarting the command reuses the same URL, the way
// prepare's stable service ports do (design.md D7 layer 2).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
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
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	portFlag := fs.Int("port", 0, "")
	openFlag := fs.Bool("open", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return 2, fmt.Errorf("%s", err.Error())
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
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}

	// Register with the shared proxy, if it's running (design.md D2/task
	// 2.4): stackfork.localhost joins every instance's route while the
	// dashboard runs, and comes back out on shutdown. Best effort, like
	// every other proxy reload - a problem here never blocks the dashboard
	// itself from serving on its own port.
	registeredWithProxy := false
	if running, _ := proxy.IsRunning(proxy.ContainerName()); running {
		if routes, err := lifecycle.GatherRoutes(newStateBase, legacyStateBase); err == nil {
			if err := dashboard.RegisterWithProxy(newStateBase, routes, port); err == nil {
				registeredWithProxy = true
			} else {
				fmt.Fprintf(stderr, "warning: proxy registration skipped: %s\n", err)
			}
		}
	}
	unregister := func() {
		if !registeredWithProxy {
			return
		}
		routes, err := lifecycle.GatherRoutes(newStateBase, legacyStateBase)
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
	go func() { errCh <- httpServer.ListenAndServe() }()

	fmt.Fprintf(stdout, "stackfork dashboard listening on http://%s\n", addr)
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return 0, err
	}
	return 0, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Run() //nolint:errcheck // best effort; the printed URL is the fallback
}
