package dashboard

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TriggerResume labels the runs that bring back what was running before a
// reboot or a Docker restart.
const TriggerResume = "resume"

// resumable picks the instances Resume starts, most recently used first: the
// person wanted them running and none of their Compose containers is. One that
// is partly up (a single container died) or whose checkout is gone is left
// alone, unless the last run was a resume that got cut off.
func resumable(insts []inventory.Instance) []inventory.Instance {
	var out []inventory.Instance
	for _, in := range insts {
		if !in.WantRunning || (in.State != inventory.StateStopped && in.State != inventory.StatePartial) {
			continue
		}
		up := false
		for _, svc := range in.Services {
			up = up || (svc.Host == nil && svc.State == inventory.StateRunning)
		}
		// a resume cut off in the middle (the dashboard quit twice) left a partial
		// stack that nobody else will finish
		cutOff := in.LastRun != nil && in.LastRun.Trigger == TriggerResume && in.LastRun.Outcome == runs.Interrupted
		if !up || cutOff {
			out = append(out, in)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Used.After(out[j].Used) })
	return out
}

// instanceDirOf is the state directory of a project.
func (s *Server) instanceDirOf(project string) string {
	if identity.IsLegacyInstance(project) {
		return filepath.Join(s.LegacyStateBase, project)
	}
	return filepath.Join(s.NewStateBase, project)
}

// Resume starts, one at a time, every instance that wants to run and is down,
// through the same up the card's Start button runs (the services the plan's
// mode starts, no seed). It prints what it starts and skips, and returns how
// many it started. The data volumes survive a reboot, so nothing is seeded.
func (s *Server) Resume(out io.Writer) (int, error) { return s.resume(out, false) }

// busyRetry adds or drops project in the set of instances resume skipped only
// because a run or the instance lock was busy; the watcher retries those.
func (s *Server) busyRetry(project string, add bool) {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	if !add {
		delete(s.retry, project)
		return
	}
	if s.retry == nil {
		s.retry = map[string]bool{}
	}
	s.retry[project] = true
}

// retrying lists the projects that wait for a retry.
func (s *Server) retrying() map[string]bool {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	out := make(map[string]bool, len(s.retry))
	for p := range s.retry {
		out[p] = true
	}
	return out
}

// resume is Resume; retry limits it to the instances that wait for a retry.
// An instance that no longer qualifies (started or stopped by a person, on
// another branch) leaves the set.
func (s *Server) resume(out io.Writer, retry bool) (int, error) {
	var only map[string]bool
	if retry {
		if only = s.retrying(); len(only) == 0 {
			return 0, nil
		}
	}
	inv, err := s.cache.get(inventory.Options{NewStateBase: s.NewStateBase, LegacyStateBase: s.LegacyStateBase, Version: s.Version})
	if err != nil {
		return 0, err
	}
	if !inv.Docker.Reachable {
		return 0, fmt.Errorf("docker is not reachable: %s", inv.Docker.Error)
	}
	started := 0
	cands := resumable(inv.Instances)
	if retry {
		for p := range only {
			s.busyRetry(p, false)
		}
		kept := cands[:0]
		for _, in := range cands {
			if only[in.Project] {
				kept = append(kept, in)
			}
		}
		cands = kept
	}
	for _, in := range cands {
		select {
		case <-s.done: // shutting down: start nothing new
			return started, nil
		default:
		}
		dir := s.instanceDirOf(in.Project)
		if why := resumeBlock(in, dir); why != "" {
			fmt.Fprintf(out, "skipped %s: %s\n", in.Project, why)
			s.busyRetry(in.Project, why == runBusy)
			continue
		}
		if !s.tryLockProject(in.Project) {
			fmt.Fprintf(out, "skipped %s: an action is in progress\n", in.Project)
			s.busyRetry(in.Project, true)
			continue
		}
		fmt.Fprintf(out, "starting %s\n", in.Project)
		resp := s.runAction(actionRequest{Action: "up", Project: in.Project, trigger: TriggerResume})
		s.unlockProject(in.Project)
		if !resp.OK {
			fmt.Fprintf(out, "failed %s: %s\n", in.Project, resp.Message)
			continue
		}
		started++
	}
	if started == 0 && !retry {
		fmt.Fprintln(out, "nothing to resume")
	}
	return started, nil
}

const runBusy = "a run is in progress"

// resumeBlock says why an instance that wants to run is left alone now: a run
// or another process holds it, or its checkout is on another branch.
func resumeBlock(in inventory.Instance, dir string) string {
	if runs.Busy(dir) || state.Held(filepath.Join(dir, ".lock")) {
		return runBusy
	}
	cur, err := exec.Command("git", "-C", in.Root, "symbolic-ref", "--short", "-q", "HEAD").Output() // also works before the first commit
	if err != nil {
		cur, err = exec.Command("git", "-C", in.Root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	}
	if err != nil || strings.TrimSpace(string(cur)) != in.Branch {
		return fmt.Sprintf("%s is not on %s now", in.Root, in.Branch)
	}
	return ""
}

// watchPolls turns each poll of Docker into "came back" events. Docker counts
// as gone only after two failed polls in a row, so one slow answer is not a
// restart.
type watchPolls struct {
	up    bool
	fails int
}

// poll records one answer; it is true when Docker has returned after being
// gone, or on the first answer.
func (w *watchPolls) poll(reachable bool) (back bool) {
	if reachable {
		w.fails = 0
		back, w.up = !w.up, true
		return back
	}
	if w.fails++; w.fails >= 2 {
		w.up = false
	}
	return false
}

// cascadeTicker is the part of lifecycle.CascadeWatcher the watch loop drives.
type cascadeTicker interface {
	Tick()
	Reset()
}

// watchEvery is the watch loop's poll of Docker.
const watchEvery = 5 * time.Second

// StartWatch runs Watch in the background; WaitWatch waits for it.
func (s *Server) StartWatch() {
	s.watch.Add(1)
	go func() {
		defer s.watch.Done()
		s.Watch()
	}()
}

// WaitWatch waits for the watcher (and the resume or cascade it is in) to end,
// or for stop to close. Close the server first so it ends.
func (s *Server) WaitWatch(stop <-chan struct{}) {
	done := make(chan struct{})
	go func() { s.watch.Wait(); close(done) }()
	select {
	case <-done:
	case <-stop:
	}
}

// Watch is the machine's one watcher, until Close: it resumes at start and
// each time Docker comes back after being out of reach, and on the other
// ticks runs the cascade watcher (plan.stop_cascade). Only one runs on a
// machine (the macOS app's dashboard and a terminal one would both act): it
// takes <state>/watch.lock for the life of the process, and another process
// retries each poll. An unreachable Docker is waited out quietly.
func (s *Server) Watch() {
	var lock *state.Lock
	defer func() {
		if lock != nil {
			lock.Release()
		}
	}()
	polls := watchPolls{}
	cw := &lifecycle.CascadeWatcher{NewBase: s.NewStateBase, LegacyBase: s.LegacyStateBase, ActionLocked: s.ActionLocked,
		Instances: func() []string { return lifecycle.InstanceDirs(s.NewStateBase, s.LegacyStateBase) }}
	var lastCascade time.Time
	for {
		if lock == nil {
			os.MkdirAll(s.NewStateBase, 0o700) //nolint:errcheck // TryLock reports it
			lock, _ = state.TryLock(filepath.Join(s.NewStateBase, "watch.lock"))
		}
		if lock != nil && s.watchTick(&polls, cw, time.Since(lastCascade) >= lifecycle.CascadeEvery) {
			lastCascade = time.Now()
		}
		select {
		case <-s.done:
			return
		case <-time.After(watchEvery):
		}
	}
}

// watchTick is one poll: resume first when Docker is back, cascade after, and
// never both on one tick. A tick that does not cascade (Docker out of reach,
// or back and resumed) resets what the cascade watcher saw, so a service
// counts as lost only after it is seen running again: nothing acts on what
// was true before Docker went away or before a resume. It reports whether
// the cascade watcher ticked.
func (s *Server) watchTick(polls *watchPolls, cw cascadeTicker, cascadeDue bool) bool {
	inv, err := s.collect()
	reachable := err == nil && inv.Docker.Reachable
	back := polls.poll(reachable)
	if !reachable || back {
		cw.Reset()
		if back {
			if cfg, err := config.Load(); err == nil && cfg.ResumeEnabled() {
				s.Resume(resumeLog{}) //nolint:errcheck // each failure is on its instance's run card
			}
		}
		return false
	}
	if cfg, err := config.Load(); err == nil && cfg.ResumeEnabled() {
		if n, _ := s.resume(resumeLog{}, true); n > 0 { // a resume tick: the cascade watcher starts over
			cw.Reset()
			return false
		}
	}
	if !cascadeDue {
		return false
	}
	cw.Tick()
	return true
}

// resumeLog sends the watcher's resume lines (what it starts, what it skips
// and why) to the dashboard's log.
type resumeLog struct{}

func (resumeLog) Write(p []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		log.Printf("resume: %s", l)
	}
	return len(p), nil
}
