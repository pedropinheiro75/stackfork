package dashboard

import "net/http"

// handleProject is the Project page's read model (docs/design/project-page/api.md).
func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
