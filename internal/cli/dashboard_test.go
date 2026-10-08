package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestStableDashboardPortIsStableAndPersisted covers task 2.3's "unit test
// for port selection": the same state dir yields the same port across
// calls, and it survives being read back after a fresh process (a plain
// re-read of the persisted file, simulated by discarding the in-memory
// value and calling again).
func TestStableDashboardPortIsStableAndPersisted(t *testing.T) {
	stateDir := t.TempDir()
	first, err := stableDashboardPort(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if first == 0 {
		t.Fatal("expected a nonzero port")
	}
	second, err := stableDashboardPort(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("expected the same port on re-read, got %d then %d", first, second)
	}

	data, err := os.ReadFile(filepath.Join(stateDir, "dashboard", "port"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(first) {
		t.Fatalf("persisted port file = %q, want %d", data, first)
	}
}

// TestStableDashboardPortReallocatesWhenBusy covers the "else allocate a
// new one" branch: a persisted port that's now held by something else must
// not be reused.
func TestStableDashboardPortReallocatesWhenBusy(t *testing.T) {
	stateDir := t.TempDir()
	first, err := stableDashboardPort(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(first))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	second, err := stableDashboardPort(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("expected a different port once %d was busy", first)
	}
}

// TestDashboardStartStop covers the rest of task 2.3: the command binds
// 127.0.0.1, serves the page, and stops cleanly (here, via context
// cancellation standing in for Ctrl-C/SIGTERM - the actual signal wiring is
// exercised by cmdDashboard's own signal.Notify, which this test doesn't
// need to re-prove).
func TestDashboardStartStop(t *testing.T) {
	// Never read the developer's real config (its scan roots can hold hundreds
	// of repos and delay startup past the deadline below).
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	stateDir := t.TempDir()
	legacyStateDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		code, _ := cmdDashboard(ctx, &stdout, &stderr, nil, stateDir, legacyStateDir)
		done <- code
	}()

	// No --port: cmdDashboard picks a stable port itself. Read it back from
	// stdout instead of guessing, polling briefly for the server to
	// announce it.
	var addr string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stdout.String(), "listening on") {
			addr = strings.TrimSpace(strings.TrimPrefix(stdout.String(), "stackfork dashboard listening on "))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if addr == "" {
		cancel()
		t.Fatalf("dashboard never announced its address; stderr=%s", stderr.String())
	}

	resp, err := http.Get(addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /: status %d", resp.StatusCode)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("cmdDashboard exited with code %d, stderr=%s", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cmdDashboard did not shut down after cancel")
	}
}

// The address is announced at once, however slow Docker/the proxy is to
// answer: registration with the proxy runs in the background.
func TestDashboardAnnouncesBeforeSlowProxyRegistration(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	old := registerDashboardWithProxy
	registerDashboardWithProxy = func(string, string, int, io.Writer) bool { time.Sleep(3 * time.Second); return false }
	defer func() { registerDashboardWithProxy = old }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	start := time.Now()
	go func() {
		code, _ := cmdDashboard(ctx, &stdout, &stderr, []string{"--port", "0"}, t.TempDir(), t.TempDir())
		done <- code
	}()
	for !strings.Contains(stdout.String(), "listening on") {
		if time.Since(start) > 1500*time.Millisecond {
			t.Fatalf("not announced within 1.5 s (registration takes 3 s); stderr=%s", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// A port that is already taken fails at once: non-zero, a clear message and
// never a "listening" line.
func TestDashboardPortInUseFailsWithoutAnnouncing(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	var stdout, stderr syncBuffer
	code := Run([]string{"--state-dir", t.TempDir(), "--legacy-state-dir", t.TempDir(), "dashboard", "--port", strconv.Itoa(port)}, &stdout, &stderr)
	if code == 0 || strings.Contains(stdout.String(), "listening") {
		t.Fatalf("code %d stdout %q", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "in use") || !strings.Contains(stderr.String(), "--port") {
		t.Fatalf("message: %q", stderr.String())
	}
}

// A second dashboard on a port a stackfork dashboard already serves says so.
func TestDashboardDetectsARunningDashboardOnThePort(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var out1, err1 syncBuffer
	done := make(chan struct{})
	go func() {
		cmdDashboard(ctx, &out1, &err1, []string{"--port", strconv.Itoa(port)}, t.TempDir(), t.TempDir())
		close(done)
	}()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(3 * time.Second); !strings.Contains(out1.String(), "listening on"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("first dashboard never started: %s", err1.String())
		}
	}
	var out2, err2 syncBuffer
	code, err := cmdDashboard(context.Background(), &out2, &err2, []string{"--port", strconv.Itoa(port)}, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "already running") || strings.Contains(out2.String(), "listening") {
		t.Fatalf("code %d err %v out %q", code, err, out2.String())
	}
}
