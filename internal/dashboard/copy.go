package dashboard

import (
	"encoding/json"
	"net/http"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// copyRequest is POST /api/copy's body (design.md D3b passwords rule, task
// 3.11). Service is required for kind database_url/cli, ignored for
// env_block (a per-stack value, not per-service).
type copyRequest struct {
	Project string `json:"project"`
	Service string `json:"service,omitempty"`
	Kind    string `json:"kind"` // database_url | env_block | cli
}

type copyResponse struct {
	Value string `json:"value"`
}

var validCopyKinds = map[string]bool{"database_url": true, "env_block": true, "cli": true}

// handleCopy is the ONLY place stackfork ever returns a real credential
// (design.md D3b): same D5 boundary as every other action (token, Origin,
// POST-only via the mux's own method routing, Host allowlist via
// withSecurity), plus Cache-Control: no-store on every response. The value
// itself is resolved by inventory.CopyValue/LiteralPassword, which return
// the placeholder unchanged for anything not a literal Compose value under
// a known key - this handler never second-guesses that refusal.
func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	var req copyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validCopyKinds[req.Kind] {
		http.Error(w, "unknown kind", http.StatusBadRequest)
		return
	}
	if req.Project == "" {
		http.Error(w, "project is required", http.StatusBadRequest)
		return
	}
	if req.Kind != "env_block" && req.Service == "" {
		http.Error(w, "service is required", http.StatusBadRequest)
		return
	}

	instanceDir, _, _, info, err := s.resolveInstance(req.Project)
	if err != nil {
		http.Error(w, "no such instance", http.StatusNotFound)
		return
	}
	inv, err := s.collect()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var services []inventory.Service
	found := false
	for _, inst := range inv.Instances {
		if inst.Project == req.Project {
			services, found = inst.Services, true
			break
		}
	}
	if !found {
		http.Error(w, "no such instance", http.StatusNotFound)
		return
	}
	svcRaw, err := inventory.ReadComposeServices(instanceDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	value, ok := inventory.CopyValue(req.Kind, req.Service, services, info.Files, svcRaw)
	if !ok {
		http.Error(w, "no such service", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(copyResponse{Value: value}) //nolint:errcheck // response already committed; nothing to do with a write error here
}
