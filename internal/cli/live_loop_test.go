package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// liveLoopFx is a repository with live mode on, one running instance with a
// pending Dockerfile change, and a fake Exec that blocks until released.
type liveLoopFx struct {
	state, legacy string
	execs         atomic.Int32
	entered       chan struct{}
	release       chan struct{}
}

func newLiveLoopFx(t *testing.T) *liveLoopFx {
	t.Helper()
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	f := &liveLoopFx{state: t.TempDir(), legacy: t.TempDir(), entered: make(chan struct{}, 4), release: make(chan struct{})}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	inst := filepath.Join(f.state, "acme-web-feature-login")
	for p, body := range map[string]string{
		filepath.Join(root, ".git", "HEAD"):    "ref: refs/heads/feature-login\n",
		filepath.Join(root, "Dockerfile"):      "FROM scratch\n",
		filepath.Join(inst, "compose.json"):    fmt.Sprintf(`{"services":{"web":{"build":{"context":%q}}}}`, root),
		filepath.Join(inst, live.BaselineFile): `{"branch":"feature-login","digests":{}}`,
	} {
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{}
	if err := cfg.SetLive(root); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	liveLoopSetup = func(l *live.Loop) {
		l.Every = 5 * time.Millisecond
		l.Now = func() time.Time {
			return time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).Add(time.Duration(clock.Add(1)) * time.Second)
		}
		l.Sync = func(string, time.Duration) repos.SyncResult { return repos.SyncResult{} }
		l.Targets = func(string) ([]live.Target, error) {
			return []live.Target{{Main: root, Root: root, Project: "acme-web-feature-login", InstanceDir: inst, Branch: "feature-login", State: "running", NewBase: f.state}}, nil
		}
		l.Exec = func(context.Context, live.Target, live.Job) (live.RunResult, error) {
			f.execs.Add(1)
			f.entered <- struct{}{}
			<-f.release
			return live.RunResult{ID: 1, Outcome: runs.Succeeded}, nil
		}
	}
	t.Cleanup(func() { liveLoopSetup = nil })
	return f
}

func startDashboard(t *testing.T, f *liveLoopFx) (cancel context.CancelFunc, out *syncBuffer, done chan int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out = &syncBuffer{}
	done = make(chan int, 1)
	go func() {
		code, _ := cmdDashboard(ctx, out, &syncBuffer{}, []string{"--port", strconv.Itoa(freePort(t))}, f.state, f.legacy)
		done <- code
	}()
	for i := 0; !strings.Contains(out.String(), "listening on"); i++ {
		if i > 300 {
			cancel()
			t.Fatal("dashboard never announced its address")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cancel, out, done
}

func TestDashboardLoopRunsOnceAndShutdownWaitsForTheLiveRun(t *testing.T) {
	f := newLiveLoopFx(t)
	cancelA, outA, doneA := startDashboard(t, f)
	defer cancelA()
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the loop never ran the pending change")
	}

	// A second dashboard on the same state runs no loop.
	cancelB, _, doneB := startDashboard(t, f)
	time.Sleep(300 * time.Millisecond)
	if f.execs.Load() != 1 {
		t.Fatalf("a second dashboard reconciled too: %d execs", f.execs.Load())
	}
	cancelB()
	<-doneB

	cancelA()
	select {
	case <-doneA:
		t.Fatal("shutdown must wait for the running live run")
	case <-time.After(300 * time.Millisecond):
	}
	if !strings.Contains(outA.String(), "waiting for 1 running action(s)") {
		t.Fatalf("shutdown must count the live run: %q", outA.String())
	}
	close(f.release)
	select {
	case <-doneA:
	case <-time.After(3 * time.Second):
		t.Fatal("dashboard did not stop after the live run ended")
	}
	if live_held(f.state) {
		t.Fatal("the lock must be released at shutdown")
	}
}

func live_held(state string) bool {
	_, err := os.Stat(live.LockPath(state))
	if err != nil {
		return false
	}
	return heldLock(live.LockPath(state))
}
