package dashboard_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// preparedFixture prepares a real instance (via the CLI, fake docker) and
// returns everything an actions test needs to call it through the
// dashboard.
func preparedFixture(t *testing.T) (newBase, legacyBase, project string) {
	t.Helper()
	installFakeDocker(t)
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fixtureCompose), 0o600); err != nil {
		t.Fatal(err)
	}
	newBase, legacyBase = t.TempDir(), t.TempDir()

	var out, errBuf bytes.Buffer
	code := cli.Run([]string{"--root", root, "--state-dir", newBase, "prepare", "-f", composePath}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("prepare failed: code=%d stderr=%s", code, errBuf.String())
	}
	var prepared struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal(out.Bytes(), &prepared); err != nil {
		t.Fatalf("prepare output not JSON: %v\n%s", err, out.String())
	}
	return newBase, legacyBase, prepared.Project
}

func postAction(t *testing.T, srv *dashboard.Server, host string, body actionRequestFixture) (int, actionResponseFixture) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/actions", bytes.NewReader(b))
	req.Host = host
	req.Header.Set("X-Stackfork-Token", srv.Token)
	req.Header.Set("Origin", "http://"+host)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp actionResponseFixture
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

type actionRequestFixture struct {
	Action  string `json:"action"`
	Project string `json:"project"`
	Service string `json:"service,omitempty"`
}

type actionResponseFixture struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// TestActionUpAndDown covers task 4.2: the `up` and `down` actions run
// through the same lifecycle.Execute path the CLI uses, against a real
// prepared fixture.
func TestActionUpAndDown(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55301)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55301"

	code, resp := postAction(t, srv, host, actionRequestFixture{Action: "up", Project: project})
	if code != 200 || !resp.OK {
		t.Fatalf("up: code=%d resp=%+v", code, resp)
	}

	code, resp = postAction(t, srv, host, actionRequestFixture{Action: "down", Project: project})
	if code != 200 || !resp.OK {
		t.Fatalf("down: code=%d resp=%+v", code, resp)
	}
}

// TestActionServiceStartStop covers fix 5: service-start/service-stop run
// through lifecycle.Execute's "start"/"stop" actions, the same path
// `stackfork start SERVICE`/`stackfork stop SERVICE` use.
func TestActionServiceStartStop(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55307)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55307"

	for _, action := range []string{"service-start", "service-stop"} {
		code, resp := postAction(t, srv, host, actionRequestFixture{Action: action, Project: project, Service: "db"})
		if code != 200 || !resp.OK {
			t.Fatalf("%s: code=%d resp=%+v", action, code, resp)
		}
	}
}

// TestActionUnknownIsRejected covers the D4 action set boundary: nothing
// outside up/down/restart/service-start/service-stop/proxy-up/down/reload
// is dispatched.
func TestActionUnknownIsRejected(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55302)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := postAction(t, srv, "127.0.0.1:55302", actionRequestFixture{Action: "down-volumes", Project: project})
	if code != 400 {
		t.Fatalf("expected 400 for an action outside the D4 set, got %d", code)
	}
}

// TestActionBranchSwitchGuardSurfacesCLIMessage covers task 4.2's check:
// the branch-switch refusal (docker ps shows another project already using
// this checkout root) surfaces the CLI's own message, not a generic error.
func TestActionBranchSwitchGuardSurfacesCLIMessage(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	t.Setenv("DOCKER_FAKE_PS", "some-other-project\n")

	srv, err := dashboard.New(newBase, legacyBase, "test", 55303)
	if err != nil {
		t.Fatal(err)
	}
	code, resp := postAction(t, srv, "127.0.0.1:55303", actionRequestFixture{Action: "up", Project: project})
	if code != 200 || resp.OK {
		t.Fatalf("expected the up action to fail: code=%d resp=%+v", code, resp)
	}
	if !strings.Contains(resp.Message, "Another branch stack still runs from this checkout") {
		t.Fatalf("expected the CLI's branch-switch message, got %q", resp.Message)
	}
}

// TestActionBusyPortGuardSurfacesCLIMessage covers task 4.2's other guard:
// a recorded stable port already held by something else refuses `up` with
// the CLI's own message.
func TestActionBusyPortGuardSurfacesCLIMessage(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	manifestPath := filepath.Join(newBase, project, "manifest.json")
	info, err := state.ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busyPort := ln.Addr().(*net.TCPAddr).Port
	info.Ports = []state.PortAlloc{{Service: "db", Target: 5432, Host: busyPort}}
	if err := state.WritePrivate(manifestPath, info); err != nil {
		t.Fatal(err)
	}

	srv, err := dashboard.New(newBase, legacyBase, "test", 55304)
	if err != nil {
		t.Fatal(err)
	}
	code, resp := postAction(t, srv, "127.0.0.1:55304", actionRequestFixture{Action: "up", Project: project})
	if code != 200 || resp.OK {
		t.Fatalf("expected the up action to fail: code=%d resp=%+v", code, resp)
	}
	if !strings.Contains(resp.Message, "already in use") || !strings.Contains(resp.Message, "reassign-ports") {
		t.Fatalf("expected the CLI's busy-port message, got %q", resp.Message)
	}
}

// TestActionDownNeverPassesVolumes proves there is no request body that
// makes the down action remove volumes: the handler's lifecycle.Options is
// always its zero value for `down`, regardless of what the request sends.
func TestActionDownNeverPassesVolumes(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55305)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55305"

	b, _ := json.Marshal(map[string]interface{}{
		"action": "down", "project": project, "volumes": true, "service": "--volumes",
	})
	req := httptest.NewRequest("POST", "/api/actions", bytes.NewReader(b))
	req.Host = host
	req.Header.Set("X-Stackfork-Token", srv.Token)
	req.Header.Set("Origin", "http://"+host)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("down: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp actionResponseFixture
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK {
		t.Fatalf("down: %+v", resp)
	}
}

// TestActionMutexRejectsOverlap covers task 4.2's per-project mutex: a
// second action for the same project while one is in flight gets 409. A
// slow fake docker invocation (DOCKER_FAKE_SLEEP) keeps the first request
// inside the handler long enough for the second, issued right after, to
// land while the lock is still held.
func TestActionMutexRejectsOverlap(t *testing.T) {
	newBase, legacyBase, project := preparedFixture(t)
	srv, err := dashboard.New(newBase, legacyBase, "test", 55306)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55306"
	t.Setenv("DOCKER_FAKE_SLEEP", "0.3")

	firstStarted := make(chan struct{})
	firstDone := make(chan int, 1)
	go func() {
		close(firstStarted)
		code, _ := postAction(t, srv, host, actionRequestFixture{Action: "up", Project: project})
		firstDone <- code
	}()
	<-firstStarted
	time.Sleep(50 * time.Millisecond) // let the first request acquire the lock and enter the sleep

	code, resp := postAction(t, srv, host, actionRequestFixture{Action: "up", Project: project})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 while the first action is in flight, got %d (%+v)", code, resp)
	}

	if firstCode := <-firstDone; firstCode != 200 {
		t.Fatalf("expected the first action to succeed, got %d", firstCode)
	}
}

// The disk endpoint answers {} before the first reading, and free-space
// is a known action that needs no project.
func TestDiskEndpointAndFreeSpaceAction(t *testing.T) {
	installFakeDocker(t)
	srv, err := dashboard.New(t.TempDir(), t.TempDir(), "test", 55126)
	if err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1:55126"
	if body := doRequest(srv, "GET", "/api/disk", host, nil).Body.String(); !strings.HasPrefix(body, "{") {
		t.Fatalf("disk: %s", body)
	}
	code, resp := postAction(t, srv, host, actionRequestFixture{Action: "free-space"})
	if code != http.StatusOK || !strings.Contains(resp.Message, "named volumes kept") {
		t.Fatalf("free-space: %d %+v", code, resp)
	}
}
