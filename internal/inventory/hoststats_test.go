package inventory

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestParseAndSumProcessTree(t *testing.T) {
	rows := parsePS(`  1     0   1000  0.0
 10     1   2048  1.5
 11    10   4096  2.0
 12    11   1024  0.5
 20     1   9999 50.0
garbage line
`)
	if len(rows) != 5 {
		t.Fatalf("rows: %+v", rows)
	}
	rss, cpu, ok := treeUsage(rows, 10)
	if !ok || rss != (2048+4096+1024)*1024 || cpu < 3.99 || cpu > 4.01 {
		t.Errorf("tree of 10: rss=%d cpu=%v ok=%v", rss, cpu, ok)
	}
	if _, _, ok := treeUsage(rows, 777); ok {
		t.Errorf("a pid ps did not list must report not found")
	}
}

// hostInstance writes an instance with one running compose service db and
// the given host procs, and returns its dir.
func hostInstance(t *testing.T, procs map[string]state.HostProc) (base, dir string) {
	t.Helper()
	installFakeDocker(t)
	base = t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-hoststat-1", root, "main", []string{"db"}, nil)
	dir = filepath.Join(base, "sf-hoststat-1")
	m, err := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.HostProcs = procs
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_FAKE_PS", psLine(t, "sf-hoststat-1", "db", "running", "Up 5 minutes", "")+"\n")
	return base, dir
}

func TestHostServiceShowsPidResourcesAndOneJobPerPoll(t *testing.T) {
	start, err := state.ProcStart(os.Getpid())
	if err != nil {
		t.Skip("no process start time on this platform")
	}
	port := startListener(t)
	base, dir := hostInstance(t, map[string]state.HostProc{
		"app": {PID: os.Getpid(), StartedAt: start, Cmdline: os.Args, Port: port},
		"api": {PID: 999999, StartedAt: "1970-01-01T00:00:00Z", Cmdline: []string{"x"}, Port: 40999},
	})
	began := time.Now().Add(-12 * time.Minute).UTC().Format(time.RFC3339)
	if err := state.WriteHostRecord(dir, state.HostRecord{Name: "app", PID: os.Getpid(), SupervisorPID: os.Getpid(), StartedAt: began, Port: port}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	old := runPS
	runPS = func() (string, error) {
		calls++
		return " " + strconv.Itoa(os.Getpid()) + " 1 319488 3.0\n", nil
	}
	t.Cleanup(func() { runPS = old })

	inst := collectOne(t, base)
	if calls != 1 {
		t.Errorf("ps calls per poll = %d, want 1", calls)
	}
	var app, api Service
	for _, s := range inst.Services {
		switch s.Name {
		case "host:app":
			app = s
		case "host:api":
			api = s
		}
	}
	h := app.Host
	if h == nil || !h.Listening || h.PID != os.Getpid() || h.RSSBytes != 319488*1024 || h.CPUPercent != 3 || h.StartedAt != began || !h.Measured {
		t.Fatalf("app host: %+v", h)
	}
	if app.State != "running" || !strings.Contains(app.Meta, "312 MB") || !strings.Contains(app.Meta, "3% CPU") || !strings.Contains(app.Meta, "up 12m") || !strings.Contains(app.Meta, "pid ") {
		t.Errorf("app meta %q state %s", app.Meta, app.State)
	}
	if api.State != "exited" || api.Host == nil || api.Host.Listening {
		t.Errorf("api: %+v", api)
	}
	if inst.State != StatePartial {
		t.Errorf("a host app not running makes the instance partial, got %s", inst.State)
	}
}

func TestExitedHostAppMakesInstancePartialWithWarning(t *testing.T) {
	base, dir := hostInstance(t, map[string]state.HostProc{
		"api": {PID: 999999, StartedAt: "1970-01-01T00:00:00Z", Cmdline: []string{"x"}, Port: 40999},
	})
	code := 1
	exited := time.Date(2026, 10, 23, 17, 12, 0, 0, time.Local)
	if err := state.WriteHostRecord(dir, state.HostRecord{Name: "api", PID: 999999, SupervisorPID: 999999, StartedAt: "1970-01-01T00:00:00Z",
		ExitedAt: exited.UTC().Format(time.RFC3339), ExitCode: &code}); err != nil {
		t.Fatal(err)
	}
	inst := collectOne(t, base)
	if inst.State != StatePartial {
		t.Fatalf("state %s", inst.State)
	}
	want := "host app api is not running (exited 1 at 17:12); start it with `stackfork host restart api` or Restart in the dashboard"
	found := false
	for _, w := range inst.Warnings {
		found = found || w == want
	}
	if !found {
		t.Errorf("warnings %q lack %q", inst.Warnings, want)
	}
	var api Service
	for _, s := range inst.Services {
		if s.Name == "host:api" {
			api = s
		}
	}
	if api.Meta != "exited 1 at 17:12" || api.Host.ExitCode == nil || *api.Host.ExitCode != 1 {
		t.Errorf("api: meta=%q host=%+v", api.Meta, api.Host)
	}
}

func TestRecycledPidIsNotRunning(t *testing.T) {
	// this very process is alive, but the recorded start time is not its own
	base, _ := hostInstance(t, map[string]state.HostProc{
		"app": {PID: os.Getpid(), StartedAt: "1970-01-01T00:00:00Z", Cmdline: os.Args, Port: startListener(t)},
	})
	inst := collectOne(t, base)
	for _, s := range inst.Services {
		if s.Name == "host:app" && s.State == "running" {
			t.Errorf("a pid with another start time must not count as running: %+v", s)
		}
	}
}

func TestComposeRunningIgnoresHostApps(t *testing.T) {
	host := Service{Name: "host:app", Kind: "host", State: "exited"}
	if !(Instance{State: StatePartial, Services: []Service{{Name: "db", State: "running"}, host}}).ComposeRunning() {
		t.Error("a down host app alone must not count against the containers")
	}
	if (Instance{State: StatePartial, Services: []Service{{Name: "db", State: "exited"}, host}}).ComposeRunning() {
		t.Error("a stopped container is not running")
	}
	if (Instance{State: StateStopped}).ComposeRunning() {
		t.Error("stopped")
	}
}

func TestAliveHostAppThatDoesNotListenYetIsStarting(t *testing.T) {
	start, err := state.ProcStart(os.Getpid())
	if err != nil {
		t.Skip("no process start time on this platform")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // a port nothing listens on
	base, _ := hostInstance(t, map[string]state.HostProc{
		"app": {PID: os.Getpid(), StartedAt: start, Cmdline: os.Args, Port: port},
	})
	inst := collectOne(t, base)
	for _, s := range inst.Services {
		if s.Name == "host:app" {
			if s.State != "starting" || s.Host == nil || s.Host.Listening || len(s.Endpoints) != 0 {
				t.Errorf("alive but not listening: %+v", s)
			}
		}
	}
	if inst.State != StateRunning {
		t.Errorf("a starting host app is not a reason for partial, got %s", inst.State)
	}
}

// A dev server may listen on all interfaces or on IPv6 only (Vite does either);
// a supervisor that is alive and a port that answers is running.
func TestHostAppListeningOnAnyInterfaceIsRunning(t *testing.T) {
	start, err := state.ProcStart(os.Getpid())
	if err != nil {
		t.Skip("no process start time on this platform")
	}
	for _, addr := range []string{"0.0.0.0:0", "[::]:0", "[::1]:0", "127.0.0.1:0"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue // no IPv6 here
		}
		port := ln.Addr().(*net.TCPAddr).Port
		base, _ := hostInstance(t, map[string]state.HostProc{
			"app": {PID: os.Getpid(), StartedAt: start, Cmdline: os.Args, Port: port},
		})
		inst := collectOne(t, base)
		ln.Close()
		for _, s := range inst.Services {
			if s.Name == "host:app" && (s.State != "running" || s.Host == nil || !s.Host.Listening) {
				t.Errorf("listening on %s: %+v", addr, s)
			}
		}
	}
}

// A record written by another supervisor (another pid) says nothing about this entry.
func TestHostRecordOfAnotherSupervisorIsUnknown(t *testing.T) {
	base, dir := hostInstance(t, map[string]state.HostProc{
		"api": {PID: 999999, StartedAt: "1970-01-01T00:00:00Z", Cmdline: []string{"x"}, Port: 40999},
	})
	code := 1
	if err := state.WriteHostRecord(dir, state.HostRecord{Name: "api", SupervisorPID: 12345, ExitedAt: "2026-10-23T17:12:00Z", ExitCode: &code}); err != nil {
		t.Fatal(err)
	}
	for _, s := range collectOne(t, base).Services {
		if s.Name == "host:api" && (s.Meta != "exited (details unknown)" || s.Host.ExitCode != nil) {
			t.Errorf("api: meta=%q host=%+v", s.Meta, s.Host)
		}
	}
}
