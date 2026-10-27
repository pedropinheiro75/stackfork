package dashboard

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestResumableSelection(t *testing.T) {
	now := time.Now()
	run := func(host bool) inventory.Service {
		s := inventory.Service{State: inventory.StateRunning}
		if host {
			s.Host = &inventory.HostInfo{}
		}
		return s
	}
	insts := []inventory.Instance{
		{Project: "old", WantRunning: true, State: inventory.StateStopped, Used: now.Add(-2 * time.Hour)},
		{Project: "new", WantRunning: true, State: inventory.StatePartial, Used: now, Services: []inventory.Service{run(true), {State: "exited"}}},
		{Project: "stopped-by-person", WantRunning: false, State: inventory.StateStopped, Used: now},
		{Project: "running", WantRunning: true, State: inventory.StateRunning, Services: []inventory.Service{run(false)}},
		{Project: "one-died", WantRunning: true, State: inventory.StatePartial, Services: []inventory.Service{run(false), {State: "exited"}}},
		{Project: "gone", WantRunning: true, State: inventory.StateMissingCheckout},
		// a resume that was cut off (dashboard quit twice) left half a stack: finish it
		{Project: "cut-off", WantRunning: true, State: inventory.StatePartial, Used: now.Add(-time.Hour), Services: []inventory.Service{run(false), {State: "created"}},
			LastRun: &inventory.LastRun{Trigger: TriggerResume, Outcome: "interrupted"}},
		{Project: "cut-off-by-person", WantRunning: true, State: inventory.StatePartial, Services: []inventory.Service{run(false), {State: "created"}},
			LastRun: &inventory.LastRun{Trigger: "dashboard", Outcome: "interrupted"}},
	}
	var got []string
	for _, in := range resumable(insts) {
		got = append(got, in.Project)
	}
	if len(got) != 3 || got[0] != "new" || got[1] != "cut-off" || got[2] != "old" {
		t.Fatalf("got %v, want [new cut-off old]", got)
	}
}

func TestWatchPollsNeedsTwoFailuresToCallDockerGone(t *testing.T) {
	var w watchPolls
	if !w.poll(true) {
		t.Fatal("first answer is a start")
	}
	if w.poll(false) {
		t.Fatal("a failed poll is no return")
	}
	if w.poll(true) {
		t.Fatal("one failed poll is not an outage, so answering again is no return")
	}
	w.poll(false)
	w.poll(false)
	if !w.poll(true) {
		t.Fatal("answering after two failures in a row is a return")
	}
}

func TestWatchLockMakesOneWatcherPerMachine(t *testing.T) {
	base := t.TempDir()
	a, _ := New(base, t.TempDir(), "t", 1)
	b, _ := New(base, t.TempDir(), "t", 1)
	a.StartWatch()
	time.Sleep(300 * time.Millisecond) // a holds watch.lock
	if l, err := state.TryLock(filepath.Join(base, "watch.lock")); err == nil {
		l.Release()
		t.Fatal("the first watcher must hold watch.lock")
	}
	b.StartWatch() // cannot take the lock: polls nothing
	a.Close()
	b.Close()
	done := make(chan struct{})
	go func() { a.WaitWatch(make(chan struct{})); b.WaitWatch(make(chan struct{})); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close must end the watchers and WaitWatch must return")
	}
	if l, err := state.TryLock(filepath.Join(base, "watch.lock")); err != nil {
		t.Fatal("the lock must be released at the end")
	} else {
		l.Release()
	}
}

// fakeCascade counts what the watch loop asks of the cascade watcher.
type fakeCascade struct{ ticks, resets int }

func (f *fakeCascade) Tick()  { f.ticks++ }
func (f *fakeCascade) Reset() { f.resets++ }

// The cascade watcher never ticks on the tick that resumed, nor while Docker
// is out of reach (one failed poll is enough), and what it saw is reset each
// time, so nothing acts on what was true before.
func TestWatchTickResumesFirstAndCascadesOnlyAfter(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = ps ] && exit ${FAKE_PS_EXIT:-0}\nexit 0\n"
	os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv, _ := New(t.TempDir(), t.TempDir(), "t", 1)
	polls, cw := &watchPolls{}, &fakeCascade{}
	tick := func(exit string, due bool) bool {
		t.Setenv("FAKE_PS_EXIT", exit)
		srv.cache = &inventoryCache{}
		return srv.watchTick(polls, cw, due)
	}
	if tick("0", true) || cw.ticks != 0 || cw.resets != 1 {
		t.Fatalf("the first answer resumes and does not cascade: %+v", cw)
	}
	if !tick("0", true) || cw.ticks != 1 {
		t.Fatalf("the next tick cascades: %+v", cw)
	}
	if tick("0", false) || cw.ticks != 1 {
		t.Fatalf("not due: %+v", cw)
	}
	tick("1", true)
	tick("1", true)
	if cw.ticks != 1 || cw.resets != 3 {
		t.Fatalf("Docker out of reach: no cascade, reset each time: %+v", cw)
	}
	if tick("0", true) || cw.ticks != 1 || cw.resets != 4 {
		t.Fatalf("Docker back: resume, no cascade on this tick: %+v", cw)
	}
	if !tick("0", true) || cw.ticks != 2 {
		t.Fatalf("after the resume tick: %+v", cw)
	}
}

// An instance skipped only because its lock was busy waits in the retry set
// and is retried when the lock is free; one skipped for its branch never is.
func TestResumeRetriesABusyInstanceButNotABranchMismatch(t *testing.T) {
	fake := t.TempDir()
	os.WriteFile(filepath.Join(fake, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0o755) //nolint:errcheck
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := t.TempDir()
	srv, _ := New(base, t.TempDir(), "t", 1)
	mk := func(project, branch string) string {
		root := t.TempDir()
		for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=a@b", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "i"}} {
			if out, err := exec.Command("git", append([]string{"-C", root}, a...)...).CombinedOutput(); err != nil {
				t.Fatalf("%v %s", err, out)
			}
		}
		dir := filepath.Join(base, project)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		m := &state.Manifest{Project: project, Root: root, Branch: branch, Hostname: project + ".localhost", WantRunning: true,
			Files: []string{"compose.yaml"}, Inputs: map[string]string{}}
		if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "compose.json"), []byte(`{"services":{"db":{"image":"postgres"}}}`), 0o600) //nolint:errcheck
		return dir
	}
	busy := mk("sf-busy-1", "main")
	mk("sf-branch-1", "other")
	lock, err := state.TryLock(filepath.Join(busy, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	srv.cache = &inventoryCache{}
	var out strings.Builder
	srv.Resume(&out) //nolint:errcheck
	got := srv.retrying()
	if !got["sf-busy-1"] || got["sf-branch-1"] || len(got) != 1 {
		t.Fatalf("retry set %v\n%s", got, out.String())
	}
	srv.cache = &inventoryCache{}
	srv.resume(io.Discard, true) //nolint:errcheck
	if !srv.retrying()["sf-busy-1"] {
		t.Fatal("still busy: stays in the set")
	}
	lock.Release()
	srv.cache = &inventoryCache{}
	srv.resume(io.Discard, true) //nolint:errcheck
	if len(srv.retrying()) != 0 {
		t.Fatalf("free again: retried and dropped, got %v", srv.retrying())
	}
}
