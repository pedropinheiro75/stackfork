package cli

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// freeTCPPort finds a free loopback port the same way net/http's httptest
// does, so live proxy tests never guess a fixed port that might already be
// taken (or, worse, collide with the real default 80 this machine may be
// serving real stacks on).
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestLiveProxyUpDown covers task 9.1's check ("live test starts and stops
// it") in isolation from routing: a distinct container name and a non-default
// port, snapshotting docker ps -a/volume ls before and after so this test
// can never be mistaken for touching the real stackfork-proxy or port 80.
func TestLiveProxyUpDown(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-updown")
	port := freeTCPPort(t)
	stateDir := t.TempDir()

	before := snapshotDocker(t)
	defer func() {
		exec.Command(bin, "--state-dir", stateDir, "proxy", "down").Run()
		after := snapshotDocker(t)
		assertSameInventory(t, before, after)
	}()

	mustRun(t, bin, "--state-dir", stateDir, "proxy", "up", "--port", strconv.Itoa(port))
	status := mustRun(t, bin, "--state-dir", stateDir, "proxy", "status")
	if !strings.Contains(status, `"running": true`) {
		t.Fatalf("expected proxy running after up, got %s", status)
	}

	mustRun(t, bin, "--state-dir", stateDir, "proxy", "down")
	status = mustRun(t, bin, "--state-dir", stateDir, "proxy", "status")
	if !strings.Contains(status, `"running": false`) {
		t.Fatalf("expected proxy stopped after down, got %s", status)
	}
	t.Log("PASS: proxy up/status/down lifecycle")
}

// TestLiveProxyRoutesInstancesAndHostRoute covers task 9.2 ("two instances
// ... each hostname reaches its own app, and stopping one leaves the other
// routed") and task 9.3 ("a host process started by the test"), by actually
// requesting through the resolver (*.localhost -> 127.0.0.1) rather than
// asserting on generated config text.
func TestLiveProxyRoutesInstancesAndHostRoute(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	modRoot := moduleRoot(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-routes")
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

	fixture, err := os.ReadFile(filepath.Join(modRoot, "testdata", "http-echo", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var instances []liveInstance
	for _, slot := range []string{"a", "b"} {
		root := filepath.Join(scratch, slot)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		mustRun(t, "git", "init", "-q", root)
		if err := os.WriteFile(filepath.Join(root, "compose.yaml"), fixture, 0o600); err != nil {
			t.Fatal(err)
		}
		li := liveInstance{binary: bin, root: root, state: stateDir, legacyState: legacyStateDir}
		li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"))
		li.run(t, "up", "--wait-timeout", "60")
		instances = append(instances, li)
	}
	defer func() {
		for _, li := range instances {
			exec.Command(li.binary, li.cli("down", "--volumes")...).Run()
		}
	}()

	mustRun(t, bin, "--state-dir", stateDir, "proxy", "up", "--port", strconv.Itoa(proxyPort))

	hostnames := make([]string, len(instances))
	for i, li := range instances {
		hostnames[i], _ = li.where(t)["hostname"].(string)
		if hostnames[i] == "" {
			t.Fatalf("instance %d: empty hostname", i)
		}
	}

	get := func(hostname string) (int, string, error) {
		resp, err := localhostClient.Get(fmt.Sprintf("http://%s:%d/", hostname, proxyPort))
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), nil
	}

	for i, h := range hostnames {
		code, body, err := get(h)
		if err != nil {
			t.Fatalf("instance %d (%s): %v", i, h, err)
		}
		if code != 200 || !strings.Contains(body, "stackfork-http-echo-fixture") {
			t.Fatalf("instance %d (%s): unexpected response %d %q", i, h, code, body)
		}
	}
	t.Log("PASS: both instances reachable through the proxy, each by its own hostname")

	// Stop one instance, reload; the other must stay routed and the stopped
	// one's route must be gone.
	instances[0].run(t, "down")
	mustRun(t, bin, "--state-dir", stateDir, "proxy", "reload")

	// Precise proof the route itself was removed (not just its backend down,
	// which would 502 either way): the regenerated Caddyfile no longer
	// mentions the stopped instance's hostname, and still mentions the
	// sibling's.
	caddyfile, err := os.ReadFile(filepath.Join(stateDir, "proxy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(caddyfile), hostnames[0]) {
		t.Fatalf("expected %s's route removed from the reloaded Caddyfile:\n%s", hostnames[0], caddyfile)
	}
	if !strings.Contains(string(caddyfile), hostnames[1]) {
		t.Fatalf("expected %s's route still in the reloaded Caddyfile:\n%s", hostnames[1], caddyfile)
	}
	code, body, err := get(hostnames[1])
	if err != nil || code != 200 || !strings.Contains(body, "stackfork-http-echo-fixture") {
		t.Fatalf("expected sibling instance still routed: code=%d body=%q err=%v", code, body, err)
	}
	t.Log("PASS: stopping one instance leaves the other routed")

	// Task 9.3: a host-run app, registered with `route add`, reachable
	// through the same proxy. This instance's only container has no HTTP
	// service at all - the route registration is what makes it routable.
	hostRoot := filepath.Join(scratch, "hostrun")
	if err := os.MkdirAll(hostRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "-q", hostRoot)
	keepalive := "services:\n  keepalive:\n    image: alpine:3.20\n    command: [\"sleep\", \"300\"]\n"
	if err := os.WriteFile(filepath.Join(hostRoot, "compose.yaml"), []byte(keepalive), 0o600); err != nil {
		t.Fatal(err)
	}
	hostLi := liveInstance{binary: bin, root: hostRoot, state: stateDir, legacyState: legacyStateDir}
	hostLi.run(t, "prepare", "-f", filepath.Join(hostRoot, "compose.yaml"))
	hostLi.run(t, "up", "--wait-timeout", "60")
	defer exec.Command(hostLi.binary, hostLi.cli("down", "--volumes")...).Run()

	// A real host process started by the test (task 9.3's check).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostPort := ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "stackfork-host-run-fixture")
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck // Close() below always returns an error from the in-flight Serve
	defer srv.Close()

	hostLi.run(t, "route", "add", "web", strconv.Itoa(hostPort))

	hostHostname, _ := hostLi.where(t)["hostname"].(string)
	code, body, err = get(hostHostname)
	if err != nil || code != 200 || !strings.Contains(body, "stackfork-host-run-fixture") {
		t.Fatalf("expected host-run route reachable: code=%d body=%q err=%v", code, body, err)
	}
	t.Log("PASS: host-run route (route add) reachable through the proxy")
}
