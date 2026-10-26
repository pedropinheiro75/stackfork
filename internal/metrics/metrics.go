// Package metrics samples CPU and memory of the stackfork containers and host
// apps into an in-memory ring buffer per service, and caches volume and image
// sizes. It backs the Project page's Resources section.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	retention   = 6 * time.Hour
	fastEvery   = 10 * time.Second
	slowEvery   = 60 * time.Second
	activeFor   = 30 * time.Second
	dfCacheFor  = 5 * time.Minute
	dfRetry     = 60 * time.Second
	dockerLimit = 60 * time.Second
)

// Target is one service the sampler may measure. The caller (the dashboard)
// builds it from the inventory; host apps carry the ps numbers the inventory
// already measured.
type Target struct {
	Project, Checkout, Service string
	Kind                       string // app, database, host-app, utility
	Where                      string // docker, host
	Image, State               string // State: running, starting, stopped
	PID                        int
	StartedAt                  time.Time
	HostMeasured               bool
	HostCPU                    float64
	HostRSS                    int64
}

type point struct {
	T   int64
	CPU float64
	Mem int64
}

// ring keeps the points of one service, oldest first, trimmed by age. The
// live points are buf[off:]; trimming moves off and the buffer is compacted in
// place once half of it is dead, so a tick never copies the whole history.
type ring struct {
	buf []point
	off int
}

func (r *ring) points() []point { return r.buf[r.off:] }

func (r *ring) add(p point, keep time.Duration) {
	r.buf = append(r.buf, p)
	cut := p.T - int64(keep.Seconds())
	for r.off < len(r.buf) && r.buf[r.off].T < cut {
		r.off++
	}
	if r.off > len(r.buf)/2 {
		n := copy(r.buf, r.buf[r.off:])
		r.buf, r.off = r.buf[:n], 0
	}
}

func (r *ring) since(t int64) []point {
	for i, p := range r.points() {
		if p.T >= t {
			return r.points()[i:]
		}
	}
	return nil
}

type latest struct {
	cpu        float64
	mem, limit int64
}

type dfData struct {
	at      time.Time
	volumes map[string]int64
	images  map[string]int64
}

// Sampler owns the buffers. Docker and Targets are injected; tests never
// start Run.
type Sampler struct {
	// Docker runs `docker ARGS` and returns stdout.
	Docker  func(args ...string) (string, error)
	Targets func() []Target
	Now     func() time.Time // nil = time.Now

	mu       sync.Mutex
	rings    map[string]*ring
	last     map[string]latest
	touched  time.Time
	started  time.Time
	sampled  time.Time
	statsErr string
	df       dfData
	dfBusy   bool
	dfTry    time.Time // last refresh attempt, so a failing docker is not retried every touch
	wake     chan struct{}
	wakeOnce sync.Once
}

// New returns a Sampler that shells out to the docker CLI.
func New(targets func() []Target) *Sampler {
	return &Sampler{Docker: runDocker, Targets: targets}
}

func runDocker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerLimit)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	return string(out), err
}

func (s *Sampler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func key(project, service string) string { return project + "\x00" + service }

func (s *Sampler) ring(project, service string) *ring {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.rings[key(project, service)]; r != nil {
		return r
	}
	return &ring{}
}

// Touch records that a client wants resources now (10 s ticks for 30 s).
// It also refreshes the volume and image sizes in the background when stale.
func (s *Sampler) Touch() {
	s.mu.Lock()
	s.touched = s.now()
	stale := !s.dfBusy && s.now().Sub(s.df.at) > dfCacheFor && s.now().Sub(s.dfTry) > dfRetry
	if stale {
		s.dfBusy, s.dfTry = true, s.now()
	}
	s.mu.Unlock()
	select { // wake Run so the page gets a sample within about a second
	case s.wakeCh() <- struct{}{}:
	default:
	}
	if stale {
		go s.RefreshDF()
	}
}

func (s *Sampler) wakeCh() chan struct{} {
	s.wakeOnce.Do(func() { s.wake = make(chan struct{}, 1) })
	return s.wake
}

// Interval is the time to the next tick.
func (s *Sampler) Interval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.now().Sub(s.touched) < activeFor {
		return fastEvery
	}
	return slowEvery
}

// StatsError is the last docker stats failure, "" when the last tick worked.
func (s *Sampler) StatsError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statsErr
}

// Run ticks until stop is closed.
func (s *Sampler) Run(stop <-chan struct{}) {
	s.Tick()
	for {
		t := time.NewTimer(s.Interval())
		select {
		case <-stop:
			t.Stop()
			return
		case <-s.wakeCh():
			t.Stop()
			s.mu.Lock()
			fresh := s.now().Sub(s.sampled) < time.Second
			s.mu.Unlock()
			if fresh {
				continue
			}
		case <-t.C:
		}
		s.Tick()
	}
}

// Tick takes one sample of every target. A service that does not run gets no
// point, which is the gap.
func (s *Sampler) Tick() {
	targets := s.Targets()
	var rows []statRow
	var err error
	for _, t := range targets {
		if t.Where == "docker" && t.State != "stopped" {
			var out string
			if out, err = s.Docker("stats", "--no-stream", "--format", "{{json .}}"); err == nil {
				rows = parseStats(out)
			}
			break
		}
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rings == nil {
		s.rings, s.last, s.started = map[string]*ring{}, map[string]latest{}, now
	}
	s.statsErr = ""
	if err != nil {
		s.statsErr = "docker stats: " + strings.TrimSpace(err.Error())
	}
	s.sampled = now
	for _, t := range targets {
		k := key(t.Project, t.Service)
		var l latest
		ok := false
		switch {
		case t.State == "stopped":
		case t.Where == "host":
			l, ok = latest{t.HostCPU, t.HostRSS, 0}, t.HostMeasured
		default:
			for _, r := range rows {
				if containerOf(r.Name, t.Project, t.Service) {
					l.cpu, l.mem, l.limit, ok = l.cpu+r.CPU, l.mem+r.Mem, r.Limit, true
				}
			}
		}
		if !ok {
			if err == nil || t.Where != "docker" { // a failed stats call keeps the last values
				delete(s.last, k)
			}
			continue
		}
		if s.rings[k] == nil {
			s.rings[k] = &ring{}
		}
		s.rings[k].add(point{now.Unix(), l.cpu, l.mem}, retention)
		s.last[k] = l
	}
	cut := now.Unix() - int64(retention.Seconds())
	for k, r := range s.rings { // a service that is gone for good frees its ring
		if p := r.points(); len(p) == 0 || p[len(p)-1].T < cut {
			delete(s.rings, k)
		}
	}
}

// containerOf reports whether name is "<project>-<service>-<n>".
func containerOf(name, project, service string) bool {
	rest, ok := strings.CutPrefix(name, project+"-"+service+"-")
	if !ok || rest == "" {
		return false
	}
	_, err := strconv.Atoi(rest)
	return err == nil
}

type statRow struct {
	Name       string
	CPU        float64
	Mem, Limit int64
}

// parseStats reads `docker stats --format '{{json .}}'`, one object per line.
// Rows without numbers ("--", a container that just stopped) are dropped.
func parseStats(out string) []statRow {
	var rows []statRow
	for _, line := range strings.Split(out, "\n") {
		var raw struct{ Name, CPUPerc, MemUsage string }
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &raw) != nil {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(raw.CPUPerc, "%"), 64)
		use, lim, ok := strings.Cut(raw.MemUsage, " / ")
		mem, ok1 := parseBytes(use)
		limit, ok2 := parseBytes(lim)
		if err != nil || !ok || !ok1 || !ok2 {
			continue
		}
		rows = append(rows, statRow{raw.Name, cpu, mem, limit})
	}
	return rows
}

var units = []struct {
	suffix string
	mult   float64
}{ // longest suffix first
	{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
	{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1},
}

// parseBytes reads "1.2GiB", "459.3MB", "0B".
func parseBytes(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			return int64(f * u.mult), err == nil
		}
	}
	return 0, false
}

// parseDF reads the one JSON object of `docker system df -v --format
// '{{json .}}'`. Untagged images are skipped; "N/A" sizes count as 0.
func parseDF(out string) (dfData, error) {
	var raw struct {
		Images  []struct{ Repository, Tag, Size string }
		Volumes []struct{ Name, Size string }
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil {
		return dfData{}, fmt.Errorf("docker df: %w", err)
	}
	d := dfData{volumes: map[string]int64{}, images: map[string]int64{}}
	for _, v := range raw.Volumes {
		n, _ := parseBytes(v.Size)
		d.volumes[v.Name] = n
	}
	for _, i := range raw.Images {
		if i.Repository == "<none>" || i.Tag == "<none>" {
			continue
		}
		n, _ := parseBytes(i.Size)
		d.images[i.Repository+":"+i.Tag] = n
	}
	return d, nil
}

// RefreshDF re-reads volume and image sizes (slow: seconds on a big daemon).
func (s *Sampler) RefreshDF() {
	out, err := s.Docker("system", "df", "-v", "--format", "{{json .}}")
	d, perr := parseDF(out)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dfBusy = false
	if err == nil && perr == nil {
		d.at = s.now()
		s.df = d
	}
}
