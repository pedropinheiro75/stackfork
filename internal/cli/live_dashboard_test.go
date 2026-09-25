package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLiveDashboardProxyIntegration covers task 2.4: while the dashboard
// runs, its own stackfork.localhost route joins the shared proxy alongside
// every instance route, and the route is removed again on shutdown
// (SIGTERM, standing in for Ctrl-C). A distinct proxy container name and a
// test port keep this away from the real stackfork-proxy and port 80.
func TestLiveDashboardProxyIntegration(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-dashboard")
	proxyPort := freeTCPPort(t)

	before := snapshotDocker(t)
	scratch := t.TempDir()
	stateDir := filepath.Join(scratch, "state")
	legacyStateDir := filepath.Join(scratch, "legacy-state")

	defer func() {
		exec.Command(bin, "--state-dir", stateDir, "proxy", "down").Run()
		after := snapshotDocker(t)
		assertSameInventory(t, before, after)
	}()

	mustRun(t, bin, "--state-dir", stateDir, "proxy", "up", "--port", strconv.Itoa(proxyPort))

	cmd := exec.Command(bin, "--state-dir", stateDir, "--legacy-state-dir", legacyStateDir, "dashboard")
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	dashboardDone := make(chan error, 1)
	go func() { dashboardDone <- cmd.Wait() }()
	exited := false
	defer func() {
		if !exited && cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			<-dashboardDone
		}
	}()

	addr, err := readAnnouncedAddr(stdout)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dashboard listening on %s", addr)

	// The proxy's Caddyfile is only rewritten on a reload it's told to do;
	// give it a moment after the dashboard registers.
	var reached bool
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := localhostClient.Get(fmt.Sprintf("http://stackfork.localhost:%d/", proxyPort))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				reached = true
				break
			}
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	if !reached {
		t.Fatalf("dashboard never reachable through the proxy at stackfork.localhost:%d: %v", proxyPort, lastErr)
	}
	t.Log("PASS: dashboard reachable through the proxy at stackfork.localhost")

	caddyfile, err := os.ReadFile(filepath.Join(stateDir, "proxy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(caddyfile), "stackfork.localhost") {
		t.Fatalf("expected stackfork.localhost in the Caddyfile while the dashboard runs:\n%s", caddyfile)
	}

	if cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-dashboardDone:
		exited = true
	case <-time.After(5 * time.Second):
		t.Fatal("dashboard did not exit after SIGTERM")
	}

	caddyfile, err = os.ReadFile(filepath.Join(stateDir, "proxy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(caddyfile), "stackfork.localhost") {
		t.Fatalf("expected stackfork.localhost route removed from the Caddyfile after shutdown:\n%s\ndashboard stderr:\n%s", caddyfile, stderrBuf.String())
	}
	t.Log("PASS: dashboard's proxy route removed on shutdown")
}

// readAnnouncedAddr reads cmdDashboard's one startup line ("stackfork
// dashboard listening on http://host:port") off the subprocess's stdout.
func readAnnouncedAddr(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "listening on") {
			return strings.TrimSpace(strings.TrimPrefix(line, "stackfork dashboard listening on ")), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("dashboard process exited without announcing its address")
}
