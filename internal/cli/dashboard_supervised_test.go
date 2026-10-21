package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// supervisedRun is one `dashboard --supervised` in this process, with a pipe
// for the "parent's" standard input.
type supervisedRun struct {
	stdin  *io.PipeWriter
	stdout *syncBuffer
	stderr *syncBuffer
	done   chan int
	cancel context.CancelFunc
	state  string
}

func startSupervised(t *testing.T, state string, args ...string) *supervisedRun {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	pr, pw := io.Pipe()
	old := dashboardStdin
	dashboardStdin = pr
	t.Cleanup(func() { dashboardStdin = old })
	ctx, cancel := context.WithCancel(context.Background())
	r := &supervisedRun{stdin: pw, stdout: &syncBuffer{}, stderr: &syncBuffer{}, done: make(chan int, 1), cancel: cancel, state: state}
	go func() {
		code, _ := cmdDashboard(ctx, r.stdout, r.stderr, append([]string{"--supervised"}, args...), state, t.TempDir())
		r.done <- code
	}()
	t.Cleanup(func() { cancel(); pw.Close() })
	return r
}

type readyJSON struct {
	Event   string `json:"event"`
	Schema  int    `json:"schema"`
	URL     string `json:"url"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// ready waits for the first stdout line and decodes it; every key must be there.
func (r *supervisedRun) ready(t *testing.T) readyJSON {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.stdout.String(), "\n") {
		if time.Now().After(deadline) {
			t.Fatalf("no ready line; stdout=%q stderr=%s", r.stdout.String(), r.stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	line, _ := bufio.NewReader(strings.NewReader(r.stdout.String())).ReadString('\n')
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &keys); err != nil {
		t.Fatalf("not JSON: %q", line)
	}
	if len(keys) != 7 {
		t.Fatalf("want 7 keys, got %v", keys)
	}
	var rl readyJSON
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rl); err != nil {
		t.Fatal(err)
	}
	if rl.Event != "ready" || rl.Schema != 1 || rl.Token == "" || len(rl.Token) != 64 || rl.PID != os.Getpid() || rl.Version != Version {
		t.Fatalf("bad ready line: %+v", rl)
	}
	if rl.URL != "http://127.0.0.1:"+strconv.Itoa(rl.Port)+"/" {
		t.Fatalf("url %q does not match port %d", rl.URL, rl.Port)
	}
	return rl
}

func (r *supervisedRun) post(t *testing.T, rl readyJSON, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", rl.URL+"api/actions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:"+strconv.Itoa(rl.Port))
	if token != "" {
		req.Header.Set("X-Stackfork-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (r *supervisedRun) waitExit(t *testing.T, within time.Duration) int {
	t.Helper()
	select {
	case code := <-r.done:
		return code
	case <-time.After(within):
		t.Fatalf("command did not exit within %s; stdout=%q", within, r.stdout.String())
		return -1
	}
}

func TestStablePortFilesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	p1, err := stablePort(dir, "port", "dashboard")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := stablePort(dir, "app-port", "dashboard-app")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("both files chose %d", p1)
	}
	for name, want := range map[string]int{"port": p1, "app-port": p2} {
		data, err := os.ReadFile(filepath.Join(dir, "dashboard", name))
		if err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(want) {
			t.Fatalf("%s = %q (%v), want %d", name, data, err, want)
		}
	}
	// the CLI wrapper keeps the old file and key
	if p, _ := stableDashboardPort(dir); p != p1 {
		t.Fatalf("stableDashboardPort = %d, want %d", p, p1)
	}
}

func TestSupervisedReadyLineAndExitOnStdinEOF(t *testing.T) {
	old := registerDashboardWithProxy
	calls := 0
	registerDashboardWithProxy = func(string, string, int, io.Writer) bool { calls++; return false }
	defer func() { registerDashboardWithProxy = old }()

	state := t.TempDir()
	os.MkdirAll(filepath.Join(state, "dashboard"), 0o700)
	os.WriteFile(filepath.Join(state, "dashboard", "port"), []byte("1\n"), 0o600)
	r := startSupervised(t, state)
	rl := r.ready(t)

	resp, err := http.Get(rl.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET url: %v %v", resp, err)
	}
	resp.Body.Close()
	if c := r.post(t, rl, "", `{"action":"bogus"}`).StatusCode; c != 403 {
		t.Fatalf("no token: %d", c)
	}
	if c := r.post(t, rl, "0123", `{"action":"bogus"}`).StatusCode; c != 403 {
		t.Fatalf("wrong token: %d", c)
	}
	if c := r.post(t, rl, rl.Token, `{"action":"bogus"}`).StatusCode; c != 400 {
		t.Fatalf("right token should pass the check and fail on the action: %d", c)
	}
	if got, _ := os.ReadFile(filepath.Join(state, "dashboard", "port")); string(got) != "1\n" {
		t.Fatalf("dashboard/port changed: %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(state, "dashboard", "app-port")); err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(rl.Port) {
		t.Fatalf("app-port = %q (%v), want %d", data, err, rl.Port)
	}
	if strings.Count(r.stdout.String(), "\n") != 1 || strings.Contains(r.stdout.String(), "listening on") {
		t.Fatalf("stdout should hold only the ready line: %q", r.stdout.String())
	}

	r.stdin.Close()
	if code := r.waitExit(t, 3*time.Second); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, r.stderr.String())
	}
	time.Sleep(50 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("proxy registration called %d times", calls)
	}
}

func TestSupervisedKeepsItsPortOnTheNextLaunch(t *testing.T) {
	state := t.TempDir()
	a := startSupervised(t, state)
	first := a.ready(t)
	a.stdin.Close()
	a.waitExit(t, 3*time.Second)
	b := startSupervised(t, state)
	if second := b.ready(t); second.Port != first.Port || second.Token == first.Token {
		t.Fatalf("port %d then %d, tokens equal=%v", first.Port, second.Port, second.Token == first.Token)
	}
}

func TestSupervisedFallsBackToKernelPortWhenRangeIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	taken := ln.Addr().(*net.TCPAddr).Port
	t.Setenv("STACKFORK_PORT_RANGE_START", strconv.Itoa(taken))
	t.Setenv("STACKFORK_PORT_RANGE_END", strconv.Itoa(taken))
	state := t.TempDir()
	r := startSupervised(t, state)
	rl := r.ready(t)
	if rl.Port == taken || rl.Port == 0 {
		t.Fatalf("port %d", rl.Port)
	}
	resp, err := http.Get(rl.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET: %v %v", resp, err)
	}
	resp.Body.Close()
	if _, err := os.Stat(filepath.Join(state, "dashboard", "app-port")); err == nil {
		t.Fatal("the kernel's port must not be recorded")
	}
}

func TestSupervisedRefusesPortAndOpen(t *testing.T) {
	for _, flag := range [][]string{{"--port", "9000"}, {"--port", "0"}, {"--open"}} {
		var stdout, stderr syncBuffer
		args := append([]string{"--state-dir", t.TempDir(), "--legacy-state-dir", t.TempDir(), "dashboard", "--supervised"}, flag...)
		if code := Run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v: exit %d, stderr=%s", flag, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "drop --port and --open") || stdout.String() != "" {
			t.Fatalf("%v: stdout=%q stderr=%q", flag, stdout.String(), stderr.String())
		}
	}
}

func TestPlainDashboardPrintsNoToken(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, stderr syncBuffer
	done := make(chan struct{})
	go func() { cmdDashboard(ctx, &stdout, &stderr, nil, t.TempDir(), t.TempDir()); close(done) }()
	for deadline := time.Now().Add(3 * time.Second); !strings.Contains(stdout.String(), "listening on"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no listening line")
		}
	}
	cancel()
	<-done
	if strings.Contains(stdout.String(), "token") || strings.Contains(stdout.String(), `"event"`) {
		t.Fatalf("plain dashboard output: %q", stdout.String())
	}
}

// Closing stdin during a running Start closes the listener at once and the
// command returns only after the Start does.
func TestSupervisedShutdownWaitsForRunningAction(t *testing.T) {
	installFakeDocker(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(root, "acme-web")
	os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agent-dev", "app.compose.yaml"), []byte(fixtureCompose), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.email=t@e.x", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"}} {
		os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644)
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := (&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	old := dashboardStartEnv
	dashboardStartEnv = func(out, errOut io.Writer, o StartOptions) error {
		close(started)
		<-release
		return nil
	}
	defer func() { dashboardStartEnv = old }()

	cfgDir := t.TempDir()
	t.Setenv("STACKFORK_CONFIG_DIR", cfgDir)
	var o, e bytes.Buffer
	if code := Run([]string{"config", "add-root", root}, &o, &e); code != 0 {
		t.Fatal(e.String())
	}
	r := startSupervised(t, t.TempDir())
	t.Setenv("STACKFORK_CONFIG_DIR", cfgDir) // startSupervised gave itself a fresh one
	rl := r.ready(t)

	body := `{"action":"start","repo":` + strconv.Quote(repo) + `,"path":` + strconv.Quote(repo) + `}`
	postDone := make(chan int, 1)
	go func() {
		for i := 0; i < 100; i++ {
			resp := r.post(t, rl, rl.Token, body)
			io.Copy(io.Discard, resp.Body)
			if resp.StatusCode != 403 { // 403 until the first scan has seen the repo
				postDone <- resp.StatusCode
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		postDone <- 403
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatalf("start never ran; stderr=%s", r.stderr.String())
	}

	r.stdin.Close()
	time.Sleep(300 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(rl.Port), 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("listener still open after stdin EOF")
	}
	select {
	case <-r.done:
		t.Fatal("command returned while the action still runs")
	default:
	}
	close(release)
	if code := r.waitExit(t, 5*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	<-postDone
}
