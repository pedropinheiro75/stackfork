package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const templateServerJS = `import { createServer } from 'node:http';
import { Readable } from 'node:stream';
createServer((req, res) => {
  if (req.url === '/download') {
    res.writeHead(200, {'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename=fixture.bin'});
    Readable.from([Buffer.from([0, 1, 2]), Buffer.from([253, 254, 255])]).pipe(res);
    return;
  }
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify({marker: process.env.RR_MARKER, db: process.env.DATABASE_URL,
    host: req.headers.host, hostname: process.env.DEV_HOSTNAME, port: process.env.NUXT_PORT, node: process.versions.node, bun: !!process.versions.bun}));
}).listen(3000, '0.0.0.0');
`

// TestLiveTemplate ports scripts/test_template.py: a real Node 22 app built
// from assets/Dockerfile, fronted by assets/nginx.conf, run through the CLI
// twice (two checkouts = two isolated stacks), checking streamed downloads,
// remembered-but-never-persisted registry credentials, named URLs, Host
// preservation and independent teardown.
func TestLiveTemplate(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	skillRoot := moduleRoot(t)

	before := snapshotDocker(t)
	var images []string
	var stacks []liveInstance
	defer func() {
		for _, li := range stacks {
			exec.Command(li.binary, li.cli("down", "--volumes")...).Run()
		}
		for _, img := range images {
			exec.Command("docker", "image", "rm", img).Run()
		}
		after := snapshotDocker(t)
		assertSameInventory(t, before, after)
	}()

	base := t.TempDir()
	stateDir := filepath.Join(base, "state")
	registry := filepath.Join(base, "fixture.npmrc")
	if err := os.WriteFile(registry, []byte("//npm.pkg.github.com/:_authToken=synthetic-template-check-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, slot := range []string{"a", "b"} {
		root := filepath.Join(base, slot, "app")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		mustRun(t, "git", "init", "-q", root)
		dest := filepath.Join(root, ".agent-dev")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Dockerfile", "entrypoint.sh", "nginx.conf", "registry.npmrc", "nuxt.compose.yaml"} {
			data, err := os.ReadFile(filepath.Join(skillRoot, "assets", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		pkgJSON, _ := json.Marshal(map[string]interface{}{
			"name":    "idv-template-fixture",
			"scripts": map[string]string{"dev": "node server.mjs"},
		})
		if err := os.WriteFile(filepath.Join(root, "package.json"), pkgJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "server.mjs"), []byte(templateServerJS), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL=do-not-load-original-env\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".npmrc"), []byte("# original checkout registry file must remain untouched\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, "docker", "run", "--rm", "--network", "none", "-v", root+":/app", "-w", "/app",
			"oven/bun:1.3.9", "bun", "install", "--lockfile-only", "--ignore-scripts")

		runtime := filepath.Join(dest, "runtime.env")
		runtimeContent := fmt.Sprintf(
			"DEV_CHECKOUT=%s\nDEV_RUNTIME_ENV=%s\nDEV_DB_NAME=fixture\nDEV_DB_IMAGE=postgres:17\nDEV_APP_REPLICAS=2\nNUXT_SESSION_PASSWORD=local-test-session-password-at-least-32-characters\nRR_MARKER=%s\nDATABASE_URL=postgres://postgres:example@db:5432/fixture\n",
			root, runtime, slot)
		if err := os.WriteFile(runtime, []byte(runtimeContent), 0o644); err != nil {
			t.Fatal(err)
		}

		li := liveInstance{binary: bin, root: root, state: stateDir}
		var prepareArgs []string
		if slot == "a" {
			prepareArgs = append(prepareArgs, "--npmrc", registry)
		}
		prepareArgs = append(prepareArgs, "prepare")
		out := mustRun(t, bin, li.cli(prepareArgs...)...)
		var prepared map[string]interface{}
		if err := json.Unmarshal([]byte(out), &prepared); err != nil {
			t.Fatalf("prepare output not JSON: %v\n%s", err, out)
		}
		where := li.where(t)
		project, _ := where["project"].(string)
		images = append(images, project+"-app:dev")
		state, _ := where["state"].(string)
		for _, name := range []string{"compose.json", "manifest.json"} {
			data, err := os.ReadFile(filepath.Join(state, name))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "synthetic-template-check-only") {
				t.Fatalf("%s leaked the npmrc token", name)
			}
		}
		stacks = append(stacks, li)
	}

	errs := make([]error, len(stacks))
	var wg sync.WaitGroup
	for i, li := range stacks {
		wg.Add(1)
		go func(i int, li liveInstance) {
			defer wg.Done()
			cmd := exec.Command(li.binary, li.cli("up", "--build", "--service", "nginx", "--wait-timeout", "120")...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs[i] = fmt.Errorf("%v\n%s", err, out)
			}
		}(i, li)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var ports []int
	for i, li := range stacks {
		slot := []string{"a", "b"}[i]
		out := li.run(t, "status")
		var apps, nginxRows []map[string]interface{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			var row map[string]interface{}
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatal(err)
			}
			switch row["Service"] {
			case "app":
				apps = append(apps, row)
			case "nginx":
				nginxRows = append(nginxRows, row)
			}
		}
		if len(apps) != 2 {
			t.Fatalf("%s: expected 2 app replicas, got %d", slot, len(apps))
		}
		for _, a := range apps {
			if a["Health"] != "healthy" {
				t.Fatalf("%s: app replica not healthy: %v", slot, a)
			}
		}
		if len(nginxRows) != 1 {
			t.Fatalf("%s: expected one nginx row, got %d", slot, len(nginxRows))
		}
		publishers, _ := nginxRows[0]["Publishers"].([]interface{})
		if len(publishers) == 0 {
			t.Fatalf("%s: nginx has no published port", slot)
		}
		portFloat, _ := publishers[0].(map[string]interface{})["PublishedPort"].(float64)
		port := int(portFloat)
		ports = append(ports, port)

		where := li.where(t)
		hostname, _ := where["hostname"].(string)
		urlsOut := li.run(t, "urls")
		want := fmt.Sprintf("http://%s:%d", hostname, port)
		if !strings.Contains(urlsOut, want) {
			t.Fatalf("%s: expected %q in urls output, got %s", slot, want, urlsOut)
		}

		data := curlResolved(t, hostname, port, "/")
		var body map[string]interface{}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("%s: response not JSON: %v\n%s", slot, err, data)
		}
		wantHost := fmt.Sprintf("%s:%d", hostname, port)
		if body["host"] != wantHost || body["hostname"] != hostname {
			t.Fatalf("%s: unexpected host/hostname: %v", slot, body)
		}
		if body["marker"] != slot || body["db"] != "postgres://postgres:example@db:5432/fixture" || body["port"] != "3000" {
			t.Fatalf("%s: unexpected response: %v", slot, body)
		}
		node, _ := body["node"].(string)
		if !strings.HasPrefix(node, "22.") {
			t.Fatalf("%s: expected Node 22, got %v", slot, node)
		}
		if b, _ := body["bun"].(bool); b {
			t.Fatalf("%s: expected the node command to run real Node, not Bun compatibility mode", slot)
		}

		downloadURL := fmt.Sprintf("http://127.0.0.1:%d/download", port)
		resp, err := http.Get(downloadURL)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		want2 := []byte{0, 1, 2, 253, 254, 255}
		if !bytes.Equal(got, want2) {
			t.Fatalf("%s: unexpected download body: %v", slot, got)
		}
		if len(resp.Header.Values("Transfer-Encoding")) > 1 {
			t.Fatalf("%s: unexpected Transfer-Encoding headers: %v", slot, resp.Header.Values("Transfer-Encoding"))
		}
		if resp.Header.Get("Content-Disposition") != "attachment; filename=fixture.bin" {
			t.Fatalf("%s: unexpected Content-Disposition: %s", slot, resp.Header.Get("Content-Disposition"))
		}

		root := li.root
		envData, _ := os.ReadFile(filepath.Join(root, ".env"))
		if string(envData) != "DATABASE_URL=do-not-load-original-env\n" {
			t.Fatalf("%s: original .env was modified: %q", slot, envData)
		}
		npmrcData, _ := os.ReadFile(filepath.Join(root, ".npmrc"))
		if string(npmrcData) != "# original checkout registry file must remain untouched\n" {
			t.Fatalf("%s: original .npmrc was modified: %q", slot, npmrcData)
		}
	}

	if ports[0] == ports[1] {
		t.Fatalf("expected distinct proxy ports, both %d", ports[0])
	}
	t.Log("PASS: real Node 22, streamed downloads through nginx, remembered registry source without persisted tokens, named URLs and proxy Host preservation, two stacks/four replicas")

	stacks[0].run(t, "down", "--volumes")
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", ports[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if body["marker"] != "b" {
		t.Fatalf("expected the untouched stack to keep serving, got %v", body)
	}
	t.Log("PASS: independent teardown; the untouched stack keeps serving")
}

// curlResolved does the equivalent of `curl --resolve hostname:port:127.0.0.1
// http://hostname:port`: sends a real HTTP request with Host: hostname, but
// dials 127.0.0.1:port instead of resolving hostname via DNS.
func curlResolved(t *testing.T, hostname string, port int, path string) []byte {
	t.Helper()
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, target)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only, http anyway
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s:%d%s", hostname, port, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, body)
	}
	return body
}
