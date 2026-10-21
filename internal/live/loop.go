package live

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Loop timing (design D4). ponytail: constants.
const (
	PollEvery    = 2 * time.Second  // watch-set poll
	TargetsEvery = 10 * time.Second // instance list and watch set refresh
)

// LockPath is the file the dashboard that runs the loop holds (design D2).
func LockPath(stateBase string) string { return filepath.Join(stateBase, "live", ".lock") }

// Loop is the dashboard's live-mode reconciler (design D2). It is idle until
// some repository has live mode on, then holds LockPath for as long as one
// has; a dashboard that cannot take the lock reports Elsewhere and tries
// again on the next tick. Everything with a clock or the outside world is a
// field, so tests run without sleeping.
type Loop struct {
	NewBase, LegacyBase, Version string
	Exec                         Exec
	ActionLocked                 func(project string) bool // the dashboard's per-project action lock; nil: never

	Config  func() (*config.Config, error) // nil: config.Load, reread when config.json changes
	Targets func(main string) ([]Target, error)
	Sync    func(checkout string, timeout time.Duration) repos.SyncResult // nil: repos.Sync
	Now     func() time.Time                                              // nil: time.Now
	Rand    func() float64                                                // fetch jitter; nil: math/rand
	Every   time.Duration                                                 // Run's tick; 0: PollEvery

	rec      *Reconciler
	lock     *state.Lock
	cfg      *config.Config
	cfgStamp string
	repos    map[string]*repoState
	inflight atomic.Int32
	busyMain atomic.Value // string: the repository whose live run is in progress

	mu   sync.Mutex
	snap Status
}

type repoState struct {
	targets   []Target
	refreshed time.Time
	err       string
	ts        map[string]*targetState
}

type targetState struct {
	fetcher   *Fetcher
	deb       *Debouncer
	poller    *Poller
	entries   []Entry
	plan      knowledge.Plan
	upstream  *runs.CauseUpstream
	retry     bool
	prepared  time.Time
	pending   []Change
	warn      string
	fetchNote string
	lastFetch time.Time
	note      string
}

// Status is what the dashboard shows (group 6) and `live status` prints.
type Status struct {
	Elsewhere bool         `json:"elsewhere,omitempty"` // another dashboard holds the loop's lock
	Repos     []RepoStatus `json:"repos"`
}

// Repository states.
const (
	StateOK     = "ok"
	StateBusy   = "busy" // a live run is in progress
	StatePaused = "paused"
)

// RepoStatus is the live state of one repository with live mode on.
type RepoStatus struct {
	Main    string         `json:"main"`
	State   string         `json:"state"`
	Reason  string         `json:"reason,omitempty"`
	Pause   *Pause         `json:"pause,omitempty"`
	Error   string         `json:"error,omitempty"`
	Targets []TargetStatus `json:"targets"`
}

// TargetStatus is one instance of the repository.
type TargetStatus struct {
	Project   string    `json:"project"`
	Root      string    `json:"root"`
	Branch    string    `json:"branch"`
	State     string    `json:"state"` // the instance state: running, partial, stopped
	Pending   []Change  `json:"pending"`
	Warnings  []string  `json:"warnings,omitempty"` // blocked fast-forward, branch switched, watch set errors
	Note      string    `json:"note,omitempty"`     // why the last reconcile did not run
	Fetch     string    `json:"fetch,omitempty"`    // the failed-fetch note with the next try
	LastFetch time.Time `json:"last_fetch,omitzero"`
	LastRun   *RunInfo  `json:"last_run,omitempty"`
}

// RunInfo is the newest live run of an instance.
type RunInfo struct {
	ID      int       `json:"id"`
	Outcome string    `json:"outcome"`
	At      time.Time `json:"at"`
}

// LastLiveRun is the newest run of the instance with trigger live, or nil.
func LastLiveRun(instanceDir string) *RunInfo {
	list, _ := runs.List(instanceDir)
	for _, r := range list {
		if r.Trigger == TriggerLive {
			at := r.EndedAt
			if at.IsZero() {
				at = r.StartedAt
			}
			return &RunInfo{ID: r.ID, Outcome: r.Outcome, At: at}
		}
	}
	return nil
}

func (l *Loop) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Running is the number of live runs in progress, for the dashboard's
// shutdown wait (it adds this to the actions it runs itself).
func (l *Loop) Running() int { return int(l.inflight.Load()) }

// Status is a snapshot, safe from any goroutine. While a live run is in
// progress its repository reads busy.
func (l *Loop) Status() Status {
	l.mu.Lock()
	s := Status{Elsewhere: l.snap.Elsewhere, Repos: append([]RepoStatus(nil), l.snap.Repos...)}
	l.mu.Unlock()
	if m, _ := l.busyMain.Load().(string); m != "" {
		for i := range s.Repos {
			if s.Repos[i].Main == m && s.Repos[i].State == StateOK {
				s.Repos[i].State = StateBusy
			}
		}
	}
	return s
}

// Run ticks until ctx ends, then releases the lock. A live run in progress
// finishes first: Exec runs inside the tick.
func (l *Loop) Run(ctx context.Context) {
	every := l.Every
	if every <= 0 {
		every = PollEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	defer l.Close()
	for {
		l.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Close releases the lock.
func (l *Loop) Close() {
	if l.lock != nil {
		l.lock.Release()
		l.lock = nil
	}
}

func (l *Loop) config() (*config.Config, error) {
	if l.Config != nil {
		return l.Config()
	}
	p, err := config.Path()
	if err != nil {
		return nil, err
	}
	stamp := "none"
	if fi, err := os.Stat(p); err == nil {
		stamp = fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size())
	}
	if l.cfg == nil || stamp != l.cfgStamp {
		c, err := config.Load()
		if err != nil {
			return nil, err
		}
		l.cfg, l.cfgStamp = c, stamp
	}
	return l.cfg, nil
}

// Tick runs one pass over every repository with live mode on.
func (l *Loop) Tick(ctx context.Context) {
	if l.rec == nil {
		l.rec = &Reconciler{ActionLocked: l.ActionLocked, Now: l.Now, Exec: func(ctx context.Context, t Target, j Job) (RunResult, error) {
			l.inflight.Add(1)
			l.busyMain.Store(t.Main)
			defer func() { l.busyMain.Store(""); l.inflight.Add(-1) }()
			return l.Exec(ctx, t, j)
		}}
		l.repos = map[string]*repoState{}
	}
	cfg, err := l.config()
	if err != nil {
		return // keep the last good state; a broken config.json is the CLI's to report
	}
	on := cfg.LiveRepos()
	if len(on) == 0 {
		l.Close()
		l.repos = map[string]*repoState{}
		l.set(Status{Repos: []RepoStatus{}})
		return
	}
	if l.lock == nil {
		os.MkdirAll(filepath.Dir(LockPath(l.NewBase)), 0o700)
		lk, err := state.TryLock(LockPath(l.NewBase))
		if errors.Is(err, state.ErrLocked) {
			l.set(Status{Elsewhere: true, Repos: []RepoStatus{}})
			return
		} else if err != nil {
			return
		}
		l.lock = lk
	}
	st := Status{Repos: []RepoStatus{}}
	for _, main := range on {
		if ctx.Err() != nil {
			break
		}
		st.Repos = append(st.Repos, l.tickRepo(ctx, main))
	}
	for m := range l.repos {
		if !cfg.LiveFor(m) {
			delete(l.repos, m)
		}
	}
	l.set(st)
}

func (l *Loop) set(s Status) {
	l.mu.Lock()
	l.snap = s
	l.mu.Unlock()
}

func (l *Loop) tickRepo(ctx context.Context, main string) RepoStatus {
	now := l.now()
	rs := l.repos[main]
	if rs == nil {
		rs = &repoState{ts: map[string]*targetState{}}
		l.repos[main] = rs
	}
	out := RepoStatus{Main: main, State: StateOK, Targets: []TargetStatus{}}
	pause, err := ReadPause(l.NewBase, main)
	if err != nil {
		out.Error = err.Error()
	}
	if rs.refreshed.IsZero() || now.Sub(rs.refreshed) >= TargetsEvery {
		list := l.Targets
		if list == nil {
			list = func(m string) ([]Target, error) { return Targets(l.NewBase, l.LegacyBase, m, l.Version) }
		}
		if ts, err := list(main); err != nil {
			rs.err = err.Error()
		} else {
			rs.targets, rs.err = ts, ""
		}
		rs.refreshed = now
	}
	out.Error = rs.err
	for _, t := range rs.targets {
		out.Targets = append(out.Targets, l.tickTarget(ctx, rs, t, pause != nil, now))
	}
	if pause != nil {
		out.State, out.Reason, out.Pause = StatePaused, pause.Reason, pause
	}
	return out
}

func (l *Loop) tickTarget(ctx context.Context, rs *repoState, t Target, paused bool, now time.Time) TargetStatus {
	ts := rs.ts[t.Root]
	if ts == nil {
		ts = &targetState{deb: NewDebouncer(), fetcher: &Fetcher{Checkout: t.Root, Sched: NewSchedule(l.Rand), Sync: l.Sync}}
		rs.ts[t.Root] = ts
	}
	st := TargetStatus{Project: t.Project, Root: t.Root, Branch: t.Branch, State: t.State, Pending: []Change{}, LastRun: LastLiveRun(t.InstanceDir)}
	running := t.State == inventory.StateRunning || t.State == inventory.StatePartial
	if !running {
		ts.poller = nil // a stopped instance is left alone; first sight again when it runs
		return st
	}
	if ts.poller == nil || ts.prepared.Before(rs.refreshed) {
		if err := l.prepare(ts, t); err != nil {
			ts.note = "watch set: " + err.Error()
			ts.prepared = rs.refreshed
			st.Warnings = []string{ts.note}
			return st
		}
		ts.prepared = rs.refreshed
	}
	ts.note = ""

	if ft := ts.fetcher.Tick(now, paused); ft.Ran {
		ts.lastFetch, ts.fetchNote = now, ft.Note
		if ft.Result.Updated {
			from := ""
			if bl, _ := LoadBaseline(t.InstanceDir); bl != nil {
				from = bl.Upstream
			}
			ts.upstream = &runs.CauseUpstream{Ref: ft.Result.Upstream, From: from, To: ft.Result.UpstreamSHA, Commits: ft.Result.Behind}
		}
		if ft.Warn != "" {
			ts.warn = ft.Warn
		} else if !ft.Result.Blocked {
			ts.warn = ""
		}
	}
	ts.pending = ts.poller.Poll()
	st.Pending, st.Fetch, st.LastFetch = append(st.Pending, ts.pending...), ts.fetchNote, ts.lastFetch
	if ts.warn != "" {
		st.Warnings = append(st.Warnings, ts.warn)
	}
	if b, detached, err := ReadBranch(t.Root); err == nil && (detached || b != t.Branch) {
		st.Warnings = append(st.Warnings, "branch switched")
	}
	if paused {
		return st
	}

	due := ts.deb.Observe(now, ts.pending)
	due = due || (ts.retry && len(ts.pending) > 0) || (ts.upstream != nil && len(ts.pending) == 0)
	if !due {
		return st
	}
	t.Changes, t.Upstream = ts.pending, ts.upstream
	out, err := l.rec.Reconcile(ctx, t)
	if err != nil {
		ts.note = err.Error()
		st.Note = ts.note
		return st
	}
	ts.retry = out.Status == Skipped
	ts.note = out.Reason
	if out.Warning != "" {
		st.Warnings = append(st.Warnings, out.Warning)
	}
	st.Note = ts.note
	switch {
	case out.Status == Settled, out.Status == Ran && out.Run.Outcome == runs.Succeeded:
		if bl, _ := LoadBaseline(t.InstanceDir); bl != nil {
			ts.poller.SetBaseline(bl.Digests)
		}
		ts.upstream, ts.pending, st.Pending = nil, nil, []Change{}
	}
	st.LastRun = LastLiveRun(t.InstanceDir)
	return st
}

// prepare derives the watch set and makes sure the baseline exists. A set
// that changed (a new override, a new Compose file) replaces the poller;
// paths that are new to the set then read as added files.
// ponytail: no silent baseline for newly watched paths.
func (l *Loop) prepare(ts *targetState, t Target) error {
	entries, plan, err := Prepare(t)
	if err != nil {
		return err
	}
	if ts.poller != nil && reflect.DeepEqual(entries, ts.entries) {
		ts.plan = plan
		return nil
	}
	bl, _ := LoadBaseline(t.InstanceDir)
	var digests map[string]string
	if bl != nil {
		digests = bl.Digests
	}
	p := NewPoller(t.Root, entries, digests)
	p.Poll()
	b, created, err := InitBaseline(t.InstanceDir, t.Branch, "", p, plan)
	if err != nil {
		return err
	}
	if !created {
		p.SetBaseline(b.Digests)
	}
	ts.poller, ts.entries, ts.plan = p, entries, plan
	return nil
}

// Prepare derives an instance's watch set and plan from its compose.json,
// manifest and knowledge.json (design D5). A target with no instance files
// (the main checkout of a repository with no instance) gets the overrides
// and knowledge paths only, which is enough to validate them.
func Prepare(t Target) ([]Entry, knowledge.Plan, error) {
	in := Inputs{Checkout: t.Root}
	if b, err := os.ReadFile(filepath.Join(t.InstanceDir, "compose.json")); err == nil && t.InstanceDir != "" {
		in.ComposeJSON = b
	}
	if t.InstanceDir != "" {
		if m, err := state.ReadManifest(filepath.Join(t.InstanceDir, "manifest.json")); err == nil && m != nil {
			in.ComposeFiles = m.Files
			if m.EnvFile != nil {
				in.EnvFile = *m.EnvFile
			}
		}
	}
	var plan knowledge.Plan
	k, err := knowledge.LoadFirst(t.Root, t.Main)
	if err != nil {
		return nil, plan, fmt.Errorf("knowledge.json: %w", err)
	}
	if k != nil {
		plan = k.Plan
		in.PackageManager, in.OtherManifests = k.Facts.PackageManager, k.Facts.OtherManifests
		in.MigrationDirs, in.AdapterFiles = k.Facts.MigrationDirs, k.AdapterFiles
		for _, w := range k.Live.Watch {
			in.Overrides.Watch = append(in.Overrides.Watch, Override(w))
		}
		in.Overrides.Ignore = k.Live.Ignore
	}
	entries, err := WatchSet(in)
	return entries, plan, err
}

// Inspect is a one-shot look at a repository for `live status`, without a
// loop: the given instances (from Targets), what is pending against each baseline, the newest
// live run. The pause comes from the caller (ReadPause).
func Inspect(newBase, main string, ts []Target) RepoStatus {
	out := RepoStatus{Main: main, State: StateOK, Targets: []TargetStatus{}}
	if p, err := ReadPause(newBase, main); err == nil && p != nil {
		out.State, out.Reason, out.Pause = StatePaused, p.Reason, p
	}
	for _, t := range ts {
		st := TargetStatus{Project: t.Project, Root: t.Root, Branch: t.Branch, State: t.State, Pending: []Change{}, LastRun: LastLiveRun(t.InstanceDir)}
		if entries, _, err := Prepare(t); err != nil {
			st.Warnings = []string{"watch set: " + err.Error()}
		} else if bl, err := LoadBaseline(t.InstanceDir); err == nil && bl != nil {
			st.Pending = NewPoller(t.Root, entries, bl.Digests).Poll()
			if st.Pending == nil {
				st.Pending = []Change{}
			}
		}
		if b, detached, err := ReadBranch(t.Root); err == nil && (detached || b != t.Branch) {
			st.Warnings = append(st.Warnings, "branch switched")
		}
		out.Targets = append(out.Targets, st)
	}
	sort.Slice(out.Targets, func(i, j int) bool { return out.Targets[i].Project < out.Targets[j].Project })
	return out
}
