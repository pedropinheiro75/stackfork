package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// handleRuns serves GET /api/runs?project=P[&id=N]: the runs of an instance as
// `stackfork runs --json` prints them, or one run (design.md D15).
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.runsInstance(w, r)
	if !ok {
		return
	}
	var v any
	if raw := r.URL.Query().Get("id"); raw != "" {
		run, ok := runByID(w, dir, raw)
		if !ok {
			return
		}
		v = run
	} else {
		list, _ := runs.List(dir)
		if list == nil {
			list = []*runs.Run{}
		}
		v = list
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint:errcheck // client gone
}

// handleRunLog serves GET /api/runs/log?project=P&id=N&step=S: the step's log
// (redacted when written). The path comes from the stored run, never from step.
func (s *Server) handleRunLog(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.runsInstance(w, r)
	if !ok {
		return
	}
	run, ok := runByID(w, dir, r.URL.Query().Get("id"))
	if !ok {
		return
	}
	b, err := runs.Log(dir, run.ID, r.URL.Query().Get("step"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b) //nolint:errcheck // client gone
}

// runsInstance resolves ?project= to an instance directory that has a manifest.
func (s *Server) runsInstance(w http.ResponseWriter, r *http.Request) (string, bool) {
	dir, _, _, _, err := s.resolveInstance(r.URL.Query().Get("project"))
	if err != nil {
		http.NotFound(w, r)
		return "", false
	}
	return dir, true
}

func runByID(w http.ResponseWriter, dir, raw string) (*runs.Run, bool) {
	if n, err := strconv.Atoi(raw); err == nil {
		if run, err := runs.Get(dir, n); err == nil {
			return run, true
		}
	}
	http.Error(w, "no such run", http.StatusNotFound)
	return nil, false
}
