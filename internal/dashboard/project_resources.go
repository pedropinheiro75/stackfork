package dashboard

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/metrics"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// samplers holds one metrics sampler per Server, created on first use.
// ponytail: a package map instead of a Server field, so the sampler stays
// inside this file; move it to Server when a second file needs it.
var samplers sync.Map // *Server -> *metrics.Sampler

// sampler returns the server's sampler and, on first use, starts it. It
// samples every instance, whichever repo asks. The goroutine ends with the
// process; tests never start it (they store a fake in samplers).
func (s *Server) sampler() *metrics.Sampler {
	if v, ok := samplers.Load(s); ok {
		return v.(*metrics.Sampler)
	}
	m := metrics.New(func() []metrics.Target {
		inv, _ := s.collect() //nolint:errcheck // an empty inventory just means nothing to sample
		var all []metrics.Target
		for _, in := range inv.Instances {
			all = append(all, resourceTargets(in)...)
		}
		return all
	})
	if v, loaded := samplers.LoadOrStore(s, m); loaded {
		return v.(*metrics.Sampler)
	}
	if !testing.Testing() {
		go m.Run(nil) // nil channel: runs until the process exits
	}
	return m
}

// resourceTargets maps an instance's services to what the sampler measures:
// containers by service name, host apps by the numbers the inventory took
// from ps.
func resourceTargets(in inventory.Instance) []metrics.Target {
	var out []metrics.Target
	for _, sv := range in.Services {
		t := metrics.Target{Project: in.Project, Checkout: in.Branch, Service: sv.Name, Where: "docker", Image: sv.Image, State: resourceState(sv.State)}
		switch {
		case sv.Host != nil:
			t.Service, t.Where, t.Kind, t.Image, t.PID = sv.Label, "host", "host-app", "", sv.Host.PID
			t.HostMeasured, t.HostCPU, t.HostRSS = sv.Host.Measured, sv.Host.CPUPercent, sv.Host.RSSBytes
			t.StartedAt, _ = time.Parse(time.RFC3339, sv.Host.StartedAt)
		case compose.DatabaseService(map[string]interface{}{"image": sv.Image}):
			t.Kind = "database"
		case compose.UtilityService(map[string]interface{}{"image": sv.Image}):
			t.Kind = "utility"
		default:
			t.Kind = "app"
		}
		out = append(out, t)
	}
	return out
}

func resourceState(s string) string {
	switch s {
	case inventory.StateRunning:
		return "running"
	case inventory.StateWaiting, "starting", "restarting", "created":
		return "starting"
	}
	return "stopped"
}

// handleProjectResources serves the Project page's resource samples
// (docs/design/project-page/api.md).
func (s *Server) handleProjectResources(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rng := map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour}[q.Get("range")]
	if rng == 0 {
		resourcesError(w, http.StatusBadRequest, "range must be 15m, 1h or 6h")
		return
	}
	res, _ := s.currentRepos()
	var repo *repos.Repo
	for i := range res.Repos {
		if res.Repos[i].Name == q.Get("name") && (q.Get("path") == "" || res.Repos[i].Path == q.Get("path")) {
			repo = &res.Repos[i]
			break
		}
	}
	if repo == nil {
		resourcesError(w, http.StatusNotFound, "unknown project "+q.Get("name"))
		return
	}
	inv, _ := s.collect() //nolint:errcheck // partial inventory is still shown
	mine := map[string]bool{}
	for _, wt := range repo.Worktrees {
		if wt.Instance != nil {
			mine[wt.Instance.Project] = true
		}
	}
	var targets []metrics.Target
	for _, in := range inv.Instances {
		if mine[in.Project] {
			targets = append(targets, resourceTargets(in)...)
		}
	}
	sm := s.sampler()
	sm.Touch()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sm.Snapshot(targets, rng)) //nolint:errcheck // client gone
}

func resourcesError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck // client gone
}
