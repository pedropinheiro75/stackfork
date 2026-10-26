package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/metrics"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

func resourcesServer(t *testing.T) *Server {
	t.Helper()
	insts := []inventory.Instance{
		{Project: "sf-a", Branch: "main", State: "running", Services: []inventory.Service{
			{Name: "app", Image: "acme-app:dev", State: "running"},
			{Name: "db", Image: "postgres:17", State: "running"},
		}},
		{Project: "sf-b", Branch: "feature/x", State: "partial", Services: []inventory.Service{
			{Name: "db", Image: "postgres:17", State: "running"},
			{Name: "host:web", Label: "web", Kind: "host", State: "running", Host: &inventory.HostInfo{
				PID: 4242, Measured: true, RSSBytes: 300 << 20, CPUPercent: 2.5, StartedAt: "2026-10-26T10:00:00Z"}},
		}},
		{Project: "sf-other", Branch: "main", State: "running", Services: []inventory.Service{{Name: "app", Image: "x:1", State: "running"}}},
	}
	s, err := New(t.TempDir(), t.TempDir(), "test", 55512)
	if err != nil {
		t.Fatal(err)
	}
	s.cache.last, s.cache.lastAt = inventory.Inventory{Instances: insts}, time.Now().Add(time.Hour)
	s.RescanAfter = time.Hour
	s.repos.res = &repos.Result{Repos: []repos.Repo{{Name: "acme", Path: "/r/acme", Worktrees: []repos.Worktree{
		{Path: "/r/acme", Branch: "main", Main: true, Instance: &inventory.InstanceRef{Project: "sf-a"}},
		{Path: "/r/acme/.worktrees/x", Branch: "feature/x", Instance: &inventory.InstanceRef{Project: "sf-b"}},
	}}}}
	s.repos.at = time.Now().Add(time.Hour)

	now := time.Unix(2_000_000, 0)
	m := &metrics.Sampler{
		Now: func() time.Time { return now },
		Targets: func() []metrics.Target {
			var all []metrics.Target
			for _, in := range insts {
				all = append(all, resourceTargets(in)...)
			}
			return all
		},
		Docker: func(args ...string) (string, error) {
			if args[0] == "stats" {
				return `{"CPUPerc":"6.25%","MemUsage":"1.5GiB / 2GiB","Name":"sf-a-app-1"}
{"CPUPerc":"0.00%","MemUsage":"100MiB / 2GiB","Name":"sf-a-db-1"}
{"CPUPerc":"1.0%","MemUsage":"50MiB / 2GiB","Name":"sf-b-db-1"}
{"CPUPerc":"90%","MemUsage":"1GiB / 2GiB","Name":"sf-other-app-1"}`, nil
			}
			return `{"Images":[{"Repository":"postgres","Tag":"17","Size":"400MB"},{"Repository":"acme-app","Tag":"dev","Size":"600MB"}],
"Volumes":[{"Name":"sf-a_pgdata","Size":"800MB"},{"Name":"sf-b_pgdata","Size":"100MB"},{"Name":"sf-other_pgdata","Size":"5GB"}]}`, nil
		},
	}
	m.Tick()
	now = now.Add(10 * time.Second)
	m.Tick()
	m.RefreshDF()
	s.sm = m
	return s
}

func getResources(s *Server, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/project/resources?"+query, nil)
	req.Host = "127.0.0.1:55512"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestProjectResourcesShape(t *testing.T) {
	rec := getResources(resourcesServer(t), "name=acme&range=1h")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var res metrics.Resources
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Range != "1h" || len(res.Series) != 4 || len(res.Services) != 4 {
		t.Fatalf("range=%s series=%d services=%d", res.Range, len(res.Series), len(res.Services))
	}
	kinds := map[string]string{}
	for _, s := range res.Series {
		kinds[s.Checkout+"/"+s.Service] = s.Kind + "/" + s.Where
	}
	want := map[string]string{"main/app": "app/docker", "main/db": "database/docker", "feature/x/db": "database/docker", "feature/x/web": "host-app/host"}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, kinds[k], v, kinds)
		}
	}
	// 6.25 + 0 + 1 + 2.5, and nothing from the other repo's instance
	if res.Totals.CPUPct != 9.8 || res.Totals.VolumesBytes != 900e6 || res.Totals.ImagesBytes != 1000e6 || res.Totals.DiskBytes != 1900e6 {
		t.Errorf("totals = %+v", res.Totals)
	}
	if len(res.Images) != 2 || len(res.Disk) != 2 || res.Disk[0].VolumesBytes != 800e6 {
		t.Errorf("images=%v disk=%v", res.Images, res.Disk)
	}
	for _, s := range res.Series {
		if len(s.Points) != 2 {
			t.Errorf("%s has %d points, want 2", s.Service, len(s.Points))
		}
	}
	if web := res.Services[3]; web.PID != 4242 || web.MemBytes != 300<<20 {
		t.Errorf("web = %+v", web)
	}
}

func TestProjectResourcesErrors(t *testing.T) {
	s := resourcesServer(t)
	if rec := getResources(s, "name=nope&range=1h"); rec.Code != 404 {
		t.Errorf("unknown project: %d", rec.Code)
	}
	if rec := getResources(s, "name=acme&range=2d"); rec.Code != 400 {
		t.Errorf("bad range: %d", rec.Code)
	}
}

// Warm starts the sampler once through the injected StartSampler, and Close stops it.
func TestWarmStartsSamplerOnceAndCloseStopsIt(t *testing.T) {
	s, err := New(t.TempDir(), t.TempDir(), "test", 55512)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	var stop <-chan struct{}
	s.StartSampler = func(m *metrics.Sampler, ch <-chan struct{}) { starts++; stop = ch }
	s.Warm()
	s.Warm()
	if starts != 1 {
		t.Fatalf("starts = %d, want 1", starts)
	}
	select {
	case <-stop:
		t.Fatal("stopped before Close")
	default:
	}
	s.Close()
	s.Close()
	select {
	case <-stop:
	default:
		t.Fatal("Close did not stop the sampler")
	}
}
