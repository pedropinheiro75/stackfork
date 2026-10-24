package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// envRequest is POST /api/env's body (add-env-overrides D7): set and unset for
// one service, or Reset for every service of the instance.
type envRequest struct {
	Project string            `json:"project"`
	Service string            `json:"service,omitempty"`
	Set     map[string]string `json:"set,omitempty"`
	Unset   []string          `json:"unset,omitempty"`
	Reset   bool              `json:"reset,omitempty"`
}

// handleEnvGet is the read model of the Environment panel: names, origins and
// the values of names that are not secret-like or vault references. A masked
// value is null in lifecycle.EnvView, so it never reaches the page.
func (s *Server) handleEnvGet(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	instanceDir, _, _, _, err := s.resolveInstance(project)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	view, err := lifecycle.EnvView(instanceDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"project": project, "services": view}) //nolint:errcheck // response already committed
}

// handleEnvPost stores overrides for one service and applies only that
// service as one run, or clears every override of the instance and applies
// the services it touched (D7). The same token and Origin checks as the other
// POSTs; a secret-like name is refused here, with no way to allow it (D5).
func (s *Server) handleEnvPost(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	var req envRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	bad := func(msg string) { writeActionResult(w, http.StatusBadRequest, actionResponse{Message: msg}) }
	instanceDir, root, branch, info, err := s.resolveInstance(req.Project)
	if err != nil {
		bad(err.Error())
		return
	}
	if !req.Reset {
		switch {
		case req.Service == "":
			bad("service is required")
			return
		case len(req.Set) == 0 && len(req.Unset) == 0:
			bad("nothing to change")
			return
		}
		names := make([]string, 0, len(req.Set))
		for n := range req.Set {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if lifecycle.SecretLikeEnvName(info, n) {
				bad(n + " looks like a secret. Map it under Settings → Secrets and let the service read the reference.")
				return
			}
		}
	}

	if !s.tryLockProject(req.Project) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "busy: an action is already running for this instance"})
		return
	}
	defer s.unlockProject(req.Project)
	if runs.Busy(instanceDir) { // an up (or any run) owns the instance; the CLI waits, the page does not (D9)
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "busy: a run is in progress for this instance; try again when it ends"})
		return
	}

	var services []string
	if req.Reset {
		touched, removed, err := lifecycle.ClearEnvOverrides(instanceDir, "")
		if err != nil {
			bad(err.Error())
			return
		}
		if removed == 0 {
			bad("no env overrides to reset")
			return
		}
		services = touched
	} else {
		if err := lifecycle.SetEnvOverrides(instanceDir, req.Service, req.Set, req.Unset, false); err != nil {
			bad(err.Error())
			return
		}
		services = []string{req.Service}
	}

	var out bytes.Buffer
	code, err := lifecycle.ApplyEnv(&out, TriggerDashboard, root, branch, req.Project, instanceDir, s.NewStateBase, s.LegacyStateBase, services)
	resp := actionResponse{OK: err == nil && code == 0, Message: fmt.Sprintf("env overrides applied to %d service(s)", len(services))}
	if latest := runs.Latest(instanceDir); latest != nil {
		resp.Run = latest.ID
	}
	if err != nil {
		resp.Message = err.Error()
	} else if code != 0 {
		resp.Message = fmt.Sprintf("apply exited with status %d", code)
	}
	writeActionResult(w, http.StatusOK, resp)
}
