package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// actionRequest is POST /api/actions' body (design.md D2).
type actionRequest struct {
	Action  string `json:"action"`
	Project string `json:"project"`
	Service string `json:"service,omitempty"`
}

type actionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// validActions is design.md D4's whole action set. Nothing outside this
// set is ever dispatched, and down here never has a way to request
// --volumes - lifecycle.Options{} always leaves Volumes false.
var validActions = map[string]bool{
	"up": true, "down": true, "restart": true,
	"proxy-up": true, "proxy-down": true, "proxy-reload": true,
}

// proxyLockKey is the mutex key for the three proxy-* actions, which are
// not scoped to one project.
const proxyLockKey = "\x00proxy"

// handleActions is the D5 security boundary (token, then Origin) plus the
// D4 action dispatch: up, down (no volumes), restart, proxy-up/down/reload,
// each running through the exact function the matching CLI subcommand
// calls (internal/lifecycle), one at a time per project (task 4.2).
func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validActions[req.Action] {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	isProxyAction := strings.HasPrefix(req.Action, "proxy-")
	if !isProxyAction && req.Project == "" {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: "project is required"})
		return
	}

	lockKey := req.Project
	if isProxyAction {
		lockKey = proxyLockKey
	}
	if !s.tryLockProject(lockKey) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "an action is already running for this instance"})
		return
	}
	defer s.unlockProject(lockKey)

	writeActionResult(w, http.StatusOK, s.runAction(req))
}

// runAction dispatches one already-locked, already-validated action.
func (s *Server) runAction(req actionRequest) actionResponse {
	switch req.Action {
	case "up", "down", "restart":
		instanceDir, root, branch, _, err := s.resolveInstance(req.Project)
		if err != nil {
			return actionResponse{Message: err.Error()}
		}
		var out bytes.Buffer
		opts := lifecycle.Options{}
		if req.Action == "restart" {
			opts.Service = req.Service
		}
		code, err := lifecycle.Execute(&out, &out, req.Action, root, branch, req.Project, instanceDir,
			s.NewStateBase, s.LegacyStateBase, opts)
		return execResult(req.Action, code, err, out.String())
	case "proxy-up":
		port, err := proxy.ReadPort(s.NewStateBase)
		if err != nil {
			return actionResponse{Message: err.Error()}
		}
		info, err := lifecycle.ProxyUp(s.NewStateBase, s.LegacyStateBase, port)
		if err != nil {
			return actionResponse{Message: err.Error()}
		}
		return actionResponse{OK: true, Message: fmt.Sprintf("proxy running on port %d", info.Port)}
	case "proxy-down":
		if _, err := lifecycle.ProxyDown(); err != nil {
			return actionResponse{Message: err.Error()}
		}
		return actionResponse{OK: true, Message: "proxy stopped"}
	case "proxy-reload":
		if err := lifecycle.ProxyReload(s.NewStateBase, s.LegacyStateBase); err != nil {
			return actionResponse{Message: err.Error()}
		}
		return actionResponse{OK: true, Message: "proxy reloaded"}
	default:
		return actionResponse{Message: "unknown action"}
	}
}

// execResult turns a lifecycle.Execute outcome into the response the page
// shows: a Go error (a refused guard, e.g. the branch-switch or busy-port
// checks) surfaces its own text unchanged, a nonzero docker exit code
// surfaces the command's own combined output, and success gets a short
// confirmation.
func execResult(action string, code int, err error, output string) actionResponse {
	if err != nil {
		return actionResponse{Message: err.Error()}
	}
	if code != 0 {
		msg := strings.TrimSpace(output)
		if msg == "" {
			msg = fmt.Sprintf("%s exited with status %d", action, code)
		}
		return actionResponse{Message: msg}
	}
	return actionResponse{OK: true, Message: action + " complete"}
}

func writeActionResult(w http.ResponseWriter, status int, resp actionResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck // response already committed; nothing to do with a write error here
}

// tokenValid checks the X-Stackfork-Token header against the per-process
// token (design.md D5).
func (s *Server) tokenValid(r *http.Request) bool {
	token := r.Header.Get("X-Stackfork-Token")
	return token != "" && token == s.Token
}

// tryLockProject and unlockProject implement design.md D4's "actions run
// one at a time per instance"; a second request while one is running for
// the same key gets 409.
func (s *Server) tryLockProject(key string) bool {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	if s.actionsRun[key] {
		return false
	}
	s.actionsRun[key] = true
	return true
}

func (s *Server) unlockProject(key string) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	delete(s.actionsRun, key)
}

// resolveInstance finds an instance's directory, root and branch from its
// project name, in whichever state directory its prefix names (task
// 4.2/4.3). Every dashboard-triggered action and log request goes through
// this, so a project name is always resolved to a real, on-disk manifest
// before any docker command runs.
func (s *Server) resolveInstance(project string) (instanceDir, root, branch string, info *state.Manifest, err error) {
	if !identity.InstancePattern.MatchString(project) {
		return "", "", "", nil, fmt.Errorf("invalid project name %q", project)
	}
	base := s.NewStateBase
	if identity.IsLegacyInstance(project) {
		base = s.LegacyStateBase
	}
	instanceDir = filepath.Join(base, project)
	info, err = state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return "", "", "", nil, err
	}
	if info == nil {
		return "", "", "", nil, fmt.Errorf("no such instance %q", project)
	}
	return instanceDir, info.Root, info.Branch, info, nil
}

// handleLogs streams a service's logs (task 4.3).
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
