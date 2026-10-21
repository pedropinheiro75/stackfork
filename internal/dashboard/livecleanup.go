package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// LiveCleanup backs the cleanup ops of POST /api/live (add-live-mode D20,
// D21). The CLI supplies it, as it does for reset: the dashboard package
// holds the security boundary (token, Origin, Host, the scan, the native
// dialog); the cleanup itself is cli code.
type LiveCleanup struct {
	// CleanText re-checks the worktree at path and returns the text of the
	// confirmation: the worktree, the instance and each volume, the branch
	// and the ignored files. It errors with the warning when the worktree is
	// not eligible.
	CleanText func(main, path string) (string, error)
	// Clean runs the cleanup after the person confirmed. actionLocked is the
	// dashboard's per-project action lock, for the D18 re-check.
	Clean func(out io.Writer, main, path string, actionLocked func(project string) bool) error
	// Keep stores the branch and head commit of the worktree at path.
	Keep func(main, path string) error
	// SettingText is the confirmation for choosing `auto` or branch deletion:
	// what is deleted without asking, and the worktrees and volumes that
	// would be deleted today.
	SettingText func(main string, cl config.Cleanup) (string, error)
}

// liveRequest is POST /api/live's body. 6.1 adds on, off and resume.
type liveRequest struct {
	Repo         string `json:"repo"`
	Op           string `json:"op"`
	Path         string `json:"path,omitempty"`
	Mode         string `json:"mode,omitempty"`
	GraceHours   *int   `json:"grace_hours,omitempty"`
	DeleteBranch *bool  `json:"delete_branch,omitempty"`
}

// cleanLockKey serializes cleanups from the page: at most one at a time.
const cleanLockKey = "\x00live-clean"

// handleLive is POST /api/live: behind the token, Origin and Host checks;
// the repository and the worktree must come from the current scan. Anything
// that deletes data or turns automation on asks with a native dialog, and
// without a dialog it writes and deletes nothing.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValid(r) {
		http.Error(w, "missing or invalid token", http.StatusForbidden)
		return
	}
	if !s.originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	var req liveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	res, _ := s.currentRepos()
	var repo *repos.Repo
	for i := range res.Repos {
		if res.Repos[i].Path == req.Repo {
			repo = &res.Repos[i]
		}
	}
	if repo == nil || !inConfiguredRoots(repo.Path) {
		writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a repo found by the scan under a configured folder"})
		return
	}
	switch req.Op {
	case "cleanup", "clean", "keep":
	default:
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: "unknown op"})
		return
	}
	if s.LiveCleanup == nil {
		writeActionResult(w, http.StatusServiceUnavailable, actionResponse{Message: "branch cleanup is not available in this build"})
		return
	}
	if req.Op == "cleanup" {
		s.liveCleanupSetting(w, repo, req)
		return
	}
	var branch string
	for _, wt := range repo.Worktrees {
		if wt.Path == req.Path && !wt.Main {
			branch = wt.Branch
		}
	}
	if req.Path == "" || branch == "" {
		writeActionResult(w, http.StatusForbidden, actionResponse{Message: "not a worktree (other than the main checkout) of this repo"})
		return
	}
	if req.Op == "keep" {
		if err := s.LiveCleanup.Keep(repo.Path, req.Path); err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error()})
			return
		}
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "kept " + branch + "; it is not suggested again until it has a new commit"})
		return
	}

	if !s.tryLockProject(cleanLockKey) {
		writeActionResult(w, http.StatusConflict, actionResponse{Message: "another cleanup is still running"})
		return
	}
	defer s.unlockProject(cleanLockKey)
	defer s.rescanAsync(false)
	text, err := s.LiveCleanup.CleanText(repo.Path, req.Path)
	if err != nil {
		writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error() + "; nothing was deleted"})
		return
	}
	approved, err := Confirm("stackfork", text)
	if err != nil {
		writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error() + "; nothing was deleted. Run: stackfork live clean " + repo.Path + " " + branch + " --yes"})
		return
	}
	if !approved {
		writeActionResult(w, http.StatusOK, actionResponse{Message: "cancelled; nothing was deleted"})
		return
	}
	var out bytes.Buffer
	if err := s.LiveCleanup.Clean(&out, repo.Path, req.Path, s.ActionLocked); err != nil {
		writeActionResult(w, http.StatusOK, actionResponse{Message: "cleanup of " + branch + " failed: " + err.Error()})
		return
	}
	writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "cleaned up " + branch})
}

// liveCleanupSetting writes the cleanup setting of a repository. Choosing
// `auto` or turning branch deletion on asks first (D21); `off`, `suggest` and
// keeping the branch only remove automation and need no dialog.
func (s *Server) liveCleanupSetting(w http.ResponseWriter, repo *repos.Repo, req liveRequest) {
	cfg, err := config.Load()
	if err != nil {
		writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
		return
	}
	was := cfg.CleanupFor(repo.Path)
	cl := was
	cl.Mode = req.Mode
	if req.GraceHours != nil {
		cl.GraceHours = *req.GraceHours
	}
	if req.DeleteBranch != nil {
		cl.DeleteBranch = *req.DeleteBranch
	}
	if err := cl.Validate(); err != nil {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: err.Error()})
		return
	}
	if cl.Mode == config.CleanupAuto || (cl.DeleteBranch && !was.DeleteBranch) {
		cmd := "stackfork live cleanup " + cl.Mode + " " + repo.Path + " --grace " + strconv.Itoa(cl.GraceHours)
		if cl.DeleteBranch {
			cmd += " --delete-branch"
		}
		text, err := s.LiveCleanup.SettingText(repo.Path, cl)
		if err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error() + "; nothing was changed"})
			return
		}
		approved, err := Confirm("stackfork", text)
		if err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error() + "; nothing was changed. Run: " + cmd})
			return
		}
		if !approved {
			writeActionResult(w, http.StatusOK, actionResponse{Message: "cancelled; nothing was changed"})
			return
		}
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if cfg, err = config.Load(); err == nil {
		if err = cfg.SetCleanup(repo.Path, cl); err == nil {
			err = cfg.Save()
		}
	}
	if err != nil {
		writeActionResult(w, http.StatusBadRequest, actionResponse{Message: err.Error()})
		return
	}
	branch := "kept"
	if cl.DeleteBranch {
		branch = "deleted"
	}
	writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: fmt.Sprintf("cleanup for %s: %s (grace %d h, local branch %s)", filepath.Base(repo.Path), cl.Mode, cl.GraceHours, branch)})
}
