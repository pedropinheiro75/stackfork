package metrics

import (
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Resources is the JSON of GET /api/project/resources (docs/design/project-page/api.md).
type Resources struct {
	Range        string    `json:"range"`
	Since        time.Time `json:"since"`
	IntervalS    int       `json:"interval_s"`
	ShortHistory bool      `json:"short_history"`
	SampledAt    string    `json:"sampled_at,omitempty"`
	StatsError   string    `json:"stats_error,omitempty"`
	Machine      Machine   `json:"machine"`
	Totals       Totals    `json:"totals"`
	Series       []Series  `json:"series"`
	Services     []Svc     `json:"services"`
	Disk         []Disk    `json:"disk"`
	Images       []Image   `json:"images"`
}

type Machine struct {
	Cores       int   `json:"cores"`
	MemoryBytes int64 `json:"memory_bytes"`
}

type Totals struct {
	CPUPct       float64 `json:"cpu_pct"`
	MemBytes     int64   `json:"mem_bytes"`
	DiskBytes    int64   `json:"disk_bytes"`
	VolumesBytes int64   `json:"volumes_bytes"`
	ImagesBytes  int64   `json:"images_bytes"`
}

type Series struct {
	Checkout string       `json:"checkout"`
	Kind     string       `json:"kind"`
	Service  string       `json:"service"`
	Where    string       `json:"where"`
	Points   [][3]float64 `json:"points"`
}

type Svc struct {
	Checkout      string  `json:"checkout"`
	Service       string  `json:"service"`
	Where         string  `json:"where"`
	Image         string  `json:"image"`
	PID           int     `json:"pid"`
	State         string  `json:"state"`
	CPUPct        float64 `json:"cpu_pct"`
	MemBytes      int64   `json:"mem_bytes"`
	MemLimitBytes int64   `json:"mem_limit_bytes"`
	DiskBytes     int64   `json:"disk_bytes"`
	UptimeS       int64   `json:"uptime_s"`
}

type Disk struct {
	Checkout     string `json:"checkout"`
	VolumesBytes int64  `json:"volumes_bytes"`
}

type Image struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// Snapshot builds the response for targets over the last rng.
func (s *Sampler) Snapshot(targets []Target, rng time.Duration) Resources {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	res := Resources{
		Range: rngName(rng), Since: s.started.UTC(), ShortHistory: s.started.IsZero() || now.Sub(s.started) < rng,
		StatsError: s.statsErr,
		Machine:    machine(),
		Series:     []Series{}, Services: []Svc{}, Disk: []Disk{}, Images: []Image{},
	}
	res.IntervalS = int(fastEvery.Seconds())
	if now.Sub(s.touched) >= activeFor {
		res.IntervalS = int(slowEvery.Seconds())
	}
	if !s.sampled.IsZero() {
		res.SampledAt = s.sampled.UTC().Format(time.RFC3339)
	}
	from := now.Add(-rng).Unix()
	projectCheckout := map[string]string{}
	var projects []string
	usedImages := map[string]bool{}
	for _, t := range targets {
		k := key(t.Project, t.Service)
		pts := [][3]float64{}
		if r := s.rings[k]; r != nil {
			for _, p := range r.since(from) {
				pts = append(pts, [3]float64{float64(p.T), round1(p.CPU), float64(p.Mem)})
			}
		}
		res.Series = append(res.Series, Series{t.Checkout, t.Kind, t.Service, t.Where, pts})
		sv := Svc{Checkout: t.Checkout, Service: t.Service, Where: t.Where, Image: t.Image, PID: t.PID, State: t.State}
		if l, ok := s.last[k]; ok && t.State != "stopped" {
			sv.CPUPct, sv.MemBytes, sv.MemLimitBytes = round1(l.cpu), l.mem, l.limit
			res.Totals.CPUPct += l.cpu
			res.Totals.MemBytes += l.mem
		}
		if !t.StartedAt.IsZero() && t.State != "stopped" {
			sv.UptimeS = max(int64(now.Sub(t.StartedAt).Seconds()), 0)
		}
		res.Services = append(res.Services, sv)
		if _, ok := projectCheckout[t.Project]; !ok {
			projectCheckout[t.Project] = t.Checkout
			projects = append(projects, t.Project)
		}
		if t.Where == "docker" && t.Image != "" {
			usedImages[t.Image] = true
		}
	}
	res.Totals.CPUPct = round1(res.Totals.CPUPct)
	for _, p := range projects { // one instance per checkout; volumes are named "<project>_<volume>"
		var n int64
		for name, size := range s.df.volumes {
			if strings.HasPrefix(name, p+"_") {
				n += size
			}
		}
		res.Disk = append(res.Disk, Disk{projectCheckout[p], n})
		res.Totals.VolumesBytes += n
	}
	for name, size := range s.df.images { // each image once, however many checkouts use it
		if usedImages[name] {
			res.Images = append(res.Images, Image{name, size})
			res.Totals.ImagesBytes += size
		}
	}
	sort.Slice(res.Images, func(i, j int) bool { return res.Images[i].Name < res.Images[j].Name })
	res.Totals.DiskBytes = res.Totals.VolumesBytes + res.Totals.ImagesBytes
	return res
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func rngName(d time.Duration) string {
	switch d {
	case 15 * time.Minute:
		return "15m"
	case time.Hour:
		return "1h"
	}
	return "6h"
}

var (
	machineOnce sync.Once
	machineVal  Machine
)

// machine is the host's cores and memory; memory is 0 where unknown (Windows).
func machine() Machine {
	machineOnce.Do(func() {
		machineVal = Machine{Cores: runtime.NumCPU()}
		switch runtime.GOOS {
		case "darwin":
			if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
				machineVal.MemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
			}
		case "linux":
			if b, err := os.ReadFile("/proc/meminfo"); err == nil {
				for _, l := range strings.Split(string(b), "\n") {
					if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemTotal:" {
						kb, _ := strconv.ParseInt(f[1], 10, 64)
						machineVal.MemoryBytes = kb * 1024
					}
				}
			}
		}
	})
	return machineVal
}
