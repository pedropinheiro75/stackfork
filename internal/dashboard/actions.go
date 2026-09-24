package dashboard

import "net/http"

// handleActions is the D5 security boundary for POST /api/actions: token
// then Origin, both required before any action code runs. The action
// dispatch itself (D4's up/down/restart/proxy-*) lands in group 4; until
// then a request that passes the boundary gets 501, never a state change.
func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// tokenValid checks the X-Stackfork-Token header against the per-process
// token (design.md D5).
func (s *Server) tokenValid(r *http.Request) bool {
	token := r.Header.Get("X-Stackfork-Token")
	return token != "" && token == s.Token
}

// handleLogs streams a service's logs (task 4.3); implemented in group 4.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
