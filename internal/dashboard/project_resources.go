package dashboard

import "net/http"

// handleProjectResources serves the Project page's resource samples
// (docs/design/project-page/api.md).
func (s *Server) handleProjectResources(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
