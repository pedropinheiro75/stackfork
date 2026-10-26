package metrics

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

var mib, gib = float64(1 << 20), float64(1 << 30)

func TestSamplerParseStats(t *testing.T) {
	out := `{"CPUPerc":"0.00%","MemUsage":"24.24MiB / 7.817GiB","Name":"sf-a-postgres-1"}
{"CPUPerc":"6.25%","MemUsage":"1.2GiB / 2GiB","Name":"sf-a-app-1"}
not json
{"CPUPerc":"--","MemUsage":"-- / --","Name":"sf-a-dead-1"}`
	got := parseStats(out)
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	if got[0].Name != "sf-a-postgres-1" || got[0].CPU != 0 || got[0].Mem != int64(24.24*mib) {
		t.Errorf("row0 = %+v", got[0])
	}
	if got[1].CPU != 6.25 || got[1].Mem != int64(1.2*gib) || got[1].Limit != 2<<30 {
		t.Errorf("row1 = %+v", got[1])
	}
}

func TestSamplerParseDF(t *testing.T) {
	out := `{"Images":[{"Repository":"postgres","Tag":"17","Size":"459.3MB"},{"Repository":"<none>","Tag":"<none>","Size":"1MB"}],
"Volumes":[{"Name":"sf-a_pgdata","Size":"854.6MB"},{"Name":"other","Size":"N/A"}]}`
	df, err := parseDF(out)
	if err != nil {
		t.Fatal(err)
	}
	if df.images["postgres:17"] != 459300000 || df.volumes["sf-a_pgdata"] != 854600000 || df.volumes["other"] != 0 {
		t.Errorf("df = %+v", df)
	}
	if _, ok := df.images["<none>:<none>"]; ok {
		t.Error("untagged image kept")
	}
	if _, err := parseDF("garbage"); err == nil {
		t.Error("want error")
	}
}

func TestSamplerRingRetentionAndRange(t *testing.T) {
	var r ring
	t0 := time.Unix(1_000_000, 0)
	for i := 0; i < 8*360; i++ { // 8 h at 10 s
		r.add(point{T: t0.Add(time.Duration(i) * 10 * time.Second).Unix(), CPU: 1, Mem: 1}, retention)
	}
	now := t0.Add(8 * 360 * 10 * time.Second)
	if n := len(r.pts); n > 6*360+1 {
		t.Fatalf("kept %d points, want at most %d", n, 6*360+1)
	}
	if got := r.since(now.Add(-15 * time.Minute).Unix()); len(got) != 90 {
		t.Errorf("15m slice = %d, want 90", len(got))
	}
}

func fixture(t *testing.T) (*Sampler, *time.Time, *string) {
	now := time.Unix(2_000_000, 0)
	stats := `{"CPUPerc":"10.0%","MemUsage":"100MiB / 1GiB","Name":"sf-a-app-1"}`
	s := &Sampler{
		Docker: func(args ...string) (string, error) {
			if args[0] == "stats" {
				if stats == "ERR" {
					return "", errors.New("docker down")
				}
				return stats, nil
			}
			return `{"Images":[{"Repository":"app","Tag":"dev","Size":"1GB"}],"Volumes":[{"Name":"sf-a_data","Size":"2GB"}]}`, nil
		},
		Now: func() time.Time { return now },
		Targets: func() []Target {
			return []Target{
				{Project: "sf-a", Checkout: "main", Service: "app", Kind: "app", Where: "docker", Image: "app:dev", State: "running"},
				{Project: "sf-a", Checkout: "main", Service: "web", Kind: "host-app", Where: "host", State: "running", PID: 5, HostMeasured: true, HostCPU: 3, HostRSS: 1 << 20},
			}
		},
	}
	return s, &now, &stats
}

func TestSamplerTickSamplesAndGap(t *testing.T) {
	s, now, stats := fixture(t)
	s.Tick()
	*now = now.Add(10 * time.Second)
	s.Tick()
	*stats = "" // container gone from docker stats: stopped, no point
	*now = now.Add(10 * time.Second)
	s.Tick()
	*stats = `{"CPUPerc":"20.0%","MemUsage":"200MiB / 1GiB","Name":"sf-a-app-1"}`
	*now = now.Add(60 * time.Second)
	s.Tick()
	app := s.ring("sf-a", "app").pts
	if len(app) != 3 {
		t.Fatalf("app points = %v, want 3 (gap on the stopped tick)", app)
	}
	if app[2].T-app[1].T != 70 {
		t.Errorf("gap = %d s, want 70", app[2].T-app[1].T)
	}
	if h := s.ring("sf-a", "web").pts; len(h) != 4 || h[0].Mem != 1<<20 {
		t.Errorf("host points = %v", h)
	}
}

func TestSamplerStatsErrorKeepsHostSamples(t *testing.T) {
	s, _, stats := fixture(t)
	*stats = "ERR"
	s.Tick()
	if s.StatsError() == "" || len(s.ring("sf-a", "app").pts) != 0 || len(s.ring("sf-a", "web").pts) != 1 {
		t.Errorf("err=%q", s.StatsError())
	}
}

func TestSamplerIntervalSwitch(t *testing.T) {
	s, now, _ := fixture(t)
	if s.Interval() != 60*time.Second {
		t.Error("idle should be 60s")
	}
	s.Touch()
	if s.Interval() != 10*time.Second {
		t.Error("touched should be 10s")
	}
	*now = now.Add(31 * time.Second)
	if s.Interval() != 60*time.Second {
		t.Error("should fall back to 60s after 30s")
	}
}

func TestSamplerSnapshotShape(t *testing.T) {
	s, now, _ := fixture(t)
	s.Tick()
	s.RefreshDF()
	*now = now.Add(10 * time.Second)
	s.Tick()
	res := s.Snapshot(s.Targets(), time.Hour)
	b, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"range", "since", "interval_s", "short_history", "machine", "totals", "series", "services", "disk", "images"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q in %s", k, b)
		}
	}
	if res.Totals.VolumesBytes != 2e9 || res.Totals.ImagesBytes != 1e9 || res.Totals.DiskBytes != 3e9 {
		t.Errorf("totals = %+v", res.Totals)
	}
	if res.Totals.CPUPct != 13 || res.Services[0].MemLimitBytes != 1<<30 {
		t.Errorf("cpu=%v svc=%+v", res.Totals.CPUPct, res.Services[0])
	}
	if !res.ShortHistory {
		t.Error("short history expected")
	}
}
