package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// localhostClient resolves any *.localhost (and bare "localhost") host to
// 127.0.0.1 itself, the way every browser is required to (RFC 6761),
// instead of trusting this machine's own DNS/mDNSResponder configuration
// to do it - `stackfork doctor` exists precisely because that OS-level
// resolution isn't guaranteed everywhere, so a live test asserting an
// endpoint's shape shouldn't depend on it either.
var localhostClient = &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if host == "localhost" || strings.HasSuffix(host, ".localhost") {
				host = "127.0.0.1"
			}
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	},
}

const dashboardFixtureCompose = `services:
  web:
    image: hashicorp/http-echo:latest
    command: ["-text=stackfork-dashboard-e2e-fixture", "-listen=:5678"]
    ports:
      - "5678:5678"
    volumes:
      - data:/data
    labels:
      io.stackfork.http: "5678"
volumes:
  data: {}
`

// TestLiveDashboardTwoScratchInstances is task 5.1's live test: two real
// scratch instances, the dashboard in front of them on a test port,
// GET /api/instances showing both with working endpoint links, a POST
// down that stops only its target and keeps its volume, and a redacted log
// stream - all against temp state dirs, torn down with docker ps -a/volume
// ls confirmed unchanged.
func TestLiveDashboardTwoScratchInstances(t *testing.T) {
	liveEnabled(t)
	before := snapshotDocker(t)
	scratch := t.TempDir()
	stateDir := filepath.Join(scratch, "state")
	legacyStateDir := filepath.Join(scratch, "legacy-state")

	var instances []liveInstance
	defer func() {
		for _, li := range instances {
			exec.Command(li.binary, li.cli("down", "--volumes")...).Run()
		}
		after := snapshotDocker(t)
		assertSameInventory(t, before, after)
	}()

	bin := stackforkBinary(t)
	for _, slot := range []string{"a", "b"} {
		root := filepath.Join(scratch, slot)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		liveInitRepo(t, root)
		if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(dashboardFixtureCompose), 0o600); err != nil {
			t.Fatal(err)
		}
		li := liveInstance{binary: bin, root: root, state: stateDir, legacyState: legacyStateDir}
		li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"))
		li.run(t, "up", "--wait-timeout", "60")
		instances = append(instances, li)
	}

	projects := make([]string, len(instances))
	for i, li := range instances {
		projects[i], _ = li.where(t)["project"].(string)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var dashStdout, dashStderr syncBuffer
	dashDone := make(chan int, 1)
	go func() {
		code, _ := cmdDashboard(ctx, &dashStdout, &dashStderr, nil, stateDir, legacyStateDir)
		dashDone <- code
	}()

	addr := waitForAnnouncedAddr(t, &dashStdout)
	t.Logf("dashboard listening on %s", addr)

	// GET /api/instances shows both, each running with a working endpoint.
	inv := fetchInstances(t, addr)
	byProject := map[string]map[string]interface{}{}
	for _, raw := range inv["instances"].([]interface{}) {
		m := raw.(map[string]interface{})
		byProject[m["project"].(string)] = m
	}
	for _, p := range projects {
		m, ok := byProject[p]
		if !ok {
			t.Fatalf("expected %s in /api/instances, got %+v", p, inv["instances"])
		}
		if m["state"] != "running" {
			t.Fatalf("expected %s running, got %+v", p, m)
		}
		endpointURL := firstHTTPEndpoint(t, m)
		body := httpGetBody(t, endpointURL)
		if !strings.Contains(body, "stackfork-dashboard-e2e-fixture") {
			t.Fatalf("endpoint %s did not serve the fixture: %q", endpointURL, body)
		}
	}
	t.Log("PASS: both scratch instances listed running with working endpoint links")

	// Logs stream through GET /api/logs and are non-empty.
	logs := httpGetBody(t, fmt.Sprintf("http://%s/api/logs?project=%s&tail=50", addr, projects[0]))
	if strings.TrimSpace(logs) == "" {
		t.Fatalf("expected non-empty logs for %s", projects[0])
	}
	t.Log("PASS: GET /api/logs streams output")

	// POST down (via the dashboard's own token) stops only the target and
	// keeps its volume; the sibling instance is unaffected.
	token := readServerToken(t, addr)
	beforeVolumes := snapshotDocker(t)
	resp := postJSONAction(t, addr, token, map[string]string{"action": "down", "project": projects[0]})
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("down action failed: %+v", resp)
	}

	// The dashboard caches Collect for 2s (design.md D2); poll past that
	// window rather than trusting the very next fetch to be fresh.
	var downState, siblingState string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		afterInv := fetchInstances(t, addr)
		for _, raw := range afterInv["instances"].([]interface{}) {
			m := raw.(map[string]interface{})
			if m["project"] == projects[0] {
				downState = m["state"].(string)
			}
			if m["project"] == projects[1] {
				siblingState = m["state"].(string)
			}
		}
		if downState == "stopped" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if downState != "stopped" {
		t.Fatalf("expected %s stopped after down, got %q", projects[0], downState)
	}
	if siblingState != "running" {
		t.Fatalf("expected sibling %s still running, got %q", projects[1], siblingState)
	}
	afterVolumes := snapshotDocker(t)
	if len(afterVolumes.volumes) != len(beforeVolumes.volumes) {
		t.Fatalf("expected down to keep volumes: before=%v after=%v", beforeVolumes.volumes, afterVolumes.volumes)
	}
	t.Log("PASS: down via the API stopped only its target and kept its volume")

	cancel()
	select {
	case <-dashDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("dashboard did not shut down; stderr=%s", dashStderr.String())
	}
}

func waitForAnnouncedAddr(t *testing.T, out *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "listening on") {
			addr := strings.TrimSpace(strings.TrimPrefix(out.String(), "stackfork dashboard listening on "))
			return strings.TrimPrefix(addr, "http://")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("dashboard never announced its address")
	return ""
}

func fetchInstances(t *testing.T, addr string) map[string]interface{} {
	t.Helper()
	body := httpGetBody(t, "http://"+addr+"/api/instances")
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("/api/instances not JSON: %v\n%s", err, body)
	}
	return m
}

func firstHTTPEndpoint(t *testing.T, instance map[string]interface{}) string {
	t.Helper()
	services, _ := instance["services"].([]interface{})
	for _, raw := range services {
		svc := raw.(map[string]interface{})
		endpoints, _ := svc["endpoints"].([]interface{})
		for _, rawEp := range endpoints {
			ep := rawEp.(map[string]interface{})
			if ep["scheme"] == "http" {
				return ep["url"].(string)
			}
		}
	}
	t.Fatalf("no http endpoint found for %+v", instance)
	return ""
}

func httpGetBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := localhostClient.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d, body %s", url, resp.StatusCode, b)
	}
	return string(b)
}

func readServerToken(t *testing.T, addr string) string {
	t.Helper()
	body := httpGetBody(t, "http://"+addr+"/")
	const marker = `name="stackfork-token" content="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatal("token meta tag not found on index page")
	}
	rest := body[idx+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatal("malformed token meta tag")
	}
	return rest[:end]
}

func postJSONAction(t *testing.T, addr, token string, body map[string]string) map[string]interface{} {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", "http://"+addr+"/api/actions", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Stackfork-Token", token)
	req.Header.Set("Origin", "http://"+addr)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("action response not JSON: %v\n%s", err, data)
	}
	return m
}
