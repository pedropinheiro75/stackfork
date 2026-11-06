package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// LiveOps backs the Live switch of the Repos panel and the live state on the
// page (add-live-mode D14). The CLI supplies it: the loop is cli-built, and
// the checks of `stackfork live on` are cli code. A nil LiveOps hides every
// live control.
type LiveOps struct {
	// Status is the loop's snapshot.
	Status func() live.Status
	// Prepare runs the checks of `live on` (watch set, secret references,
	// trust) before any dialog opens and returns the first refusal.
	Prepare func(main string) error
	// Enable writes the setting and clears a pause, after the person agreed.
	Enable func(main string) error
	// Resume clears a pause; it refuses with the step diff while the plan
	// still needs trust.
	Resume func(main string) error
}

// liveView is one repository's live state: part of the Repos fragment and of
// GET /api/repos (add-live-mode D14).
type liveView struct {
	Show     bool           `json:"-"` // LiveOps is set: draw the controls
	Enabled  bool           `json:"enabled"`
	State    string         `json:"state"` // off, ok, busy, paused, elsewhere
	Reason   string         `json:"reason,omitempty"`
	Kind     string         `json:"kind,omitempty"` // the pause kind: failed, trust, rate, plan, refused
	Run      int            `json:"run,omitempty"`
	Diff     []string       `json:"diff,omitempty"`
	Pending  map[string]int `json:"pending,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Cleanup  *cleanupView   `json:"cleanup,omitempty"`

	Repo    string                  `json:"-"`
	Setting config.Cleanup          `json:"-"` // the selector values
	Chip    chipView                `json:"-"`
	Targets map[string]worktreeLive `json:"-"` // by worktree path
}

// Trust tells a pause that needs the plan trusted on this machine.
func (v liveView) Trust() bool { return v.State == "paused" && v.Kind == live.PauseTrust }

// worktreeLive is what a worktree row says about live mode.
type worktreeLive struct {
	Behind   string // the warning behind the STATE cell title
	Note     string // the attention note under the row
	Command  string // the command to copy
	Switched bool
}

// cleanupView is the cleanup selector and the callouts of a repository.
type cleanupView struct {
	Mode         string             `json:"mode"`
	GraceHours   int                `json:"grace_hours"`
	DeleteBranch bool               `json:"delete_branch"`
	Fetch        string             `json:"fetch,omitempty"`
	Candidates   []cleanupCandidate `json:"candidates,omitempty"`
}

// cleanupCandidate is one callout: PR merged, Cleanup in N h, or a warning.
type cleanupCandidate struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
	State  string `json:"state"`
	Title  string `json:"title"`
	PR     int    `json:"pr,omitempty"`
	URL    string `json:"url,omitempty"`
	Ask    string `json:"ask,omitempty"` // suggest, auto or "" (a warning)
	// Project is the worktree's instance, for the VersionCard.
	Project string `json:"project,omitempty"`
}

// note is the card's view of the candidate.
func (c cleanupCandidate) note(repo string) *inventory.CleanupNote {
	url := c.URL
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") { // app.js links only these
		url = ""
	}
	return &inventory.CleanupNote{Repo: repo, Path: c.Path, Branch: c.Branch, Title: c.Title, PR: c.PR, URL: url, Ask: c.Ask}
}

// CleanupFor is the callout of the worktree at path, or nil.
func (v liveView) CleanupFor(path string) *cleanupCandidate {
	if v.Cleanup != nil {
		for i := range v.Cleanup.Candidates {
			if v.Cleanup.Candidates[i].Path == path {
				return &v.Cleanup.Candidates[i]
			}
		}
	}
	return nil
}

// CleanupNoteFor is the callout of the worktree at path, as the card shows it.
func (v liveView) CleanupNoteFor(path string) *inventory.CleanupNote {
	if c := v.CleanupFor(path); c != nil {
		return c.note(v.Repo)
	}
	return nil
}

// WorktreeFor is the live note of the worktree at path.
func (v liveView) WorktreeFor(path string) *worktreeLive {
	if w, ok := v.Targets[path]; ok {
		return &w
	}
	return nil
}

// cleanupIn says when an automatic cleanup starts: "Cleanup in 23 h".
func cleanupIn(at, now time.Time) string {
	d := at.Sub(now)
	if d <= time.Hour {
		return "Cleanup in under 1 h"
	}
	return fmt.Sprintf("Cleanup in %d h", int((d+time.Hour-1)/time.Hour))
}

// liveViews builds the live state of every repository from the config and
// the loop's snapshot.
func (s *Server) liveViews(rs []repos.Repo, cfg *config.Config) map[string]liveView {
	out := map[string]liveView{}
	if s.Live == nil {
		return out
	}
	var st live.Status
	if s.Live.Status != nil {
		st = s.Live.Status()
	}
	now := time.Now()
	for _, r := range rs {
		v := liveView{Show: true, Repo: r.Path, State: "off", Chip: chipView{Class: "off", Glyph: glyphOff, Label: "Off"}}
		if cfg != nil {
			v.Enabled = cfg.LiveFor(r.Path)
			v.Setting = cfg.CleanupFor(r.Path)
		}
		if v.Enabled {
			v.State = live.StateOK
			var rst *live.RepoStatus
			for i := range st.Repos {
				if st.Repos[i].Main == r.Path {
					rst = &st.Repos[i]
				}
			}
			switch {
			case st.Elsewhere:
				v.State = "elsewhere"
			case rst != nil:
				v.State = rst.State
				v.Reason = rst.Reason
				if rst.Pause != nil {
					v.Kind, v.Run, v.Diff = rst.Pause.Kind, rst.Pause.Run, rst.Pause.Diff
					v.Reason = rst.Pause.Reason
				}
			}
			switch v.State {
			case live.StateBusy:
				v.Chip = chipView{Class: "busy", Glyph: glyphBusy, Label: "Reconciling…", Title: "a live run is in progress"}
			case live.StatePaused:
				v.Chip = chipView{Class: "attention", Glyph: glyphAttention, Label: "Paused"}
				if v.Kind == live.PauseTrust {
					v.Reason = "Plan changed; host steps need trust"
				}
			case "elsewhere":
				v.Chip = chipView{Class: "off", Glyph: glyphOff, Label: "In another dashboard", Title: "another dashboard holds the live mode lock"}
			default:
				v.Chip = chipView{Class: "ok", Glyph: glyphOK, Label: "Live"}
			}
			if rst != nil {
				v.fill(r, rst, now)
			}
		}
		out[r.Path] = v
	}
	return out
}

// fill copies pending changes, worktree warnings and the cleanup state.
func (v *liveView) fill(r repos.Repo, rst *live.RepoStatus, now time.Time) {
	v.Targets = map[string]worktreeLive{}
	for _, t := range rst.Targets {
		for _, c := range t.Pending {
			if v.Pending == nil {
				v.Pending = map[string]int{}
			}
			v.Pending[c.Category]++
		}
		w := worktreeLive{}
		for _, text := range t.Warnings {
			v.Warnings = append(v.Warnings, filepath.Base(t.Root)+": "+text)
			switch {
			case text == "branch switched":
				w.Switched = true
			case strings.HasPrefix(text, "warning:"):
				w.Behind, w.Note = "Behind, not updated", strings.TrimSpace(strings.TrimPrefix(text, "warning:"))
				if i := strings.Index(text, "git -C "); i >= 0 {
					w.Command = text[i:]
				}
			default:
				w.Note = text
			}
		}
		if w != (worktreeLive{}) {
			v.Targets[t.Root] = w
		}
	}
	if rst.Cleanup == nil || rst.Cleanup.Mode == config.CleanupOff {
		return
	}
	cv := &cleanupView{Mode: rst.Cleanup.Mode, GraceHours: rst.Cleanup.GraceHours, DeleteBranch: rst.Cleanup.DeleteBranch, Fetch: rst.Cleanup.Fetch}
	for _, c := range rst.Cleanup.Candidates {
		if c.Kept {
			continue
		}
		cc := cleanupCandidate{Branch: c.Branch, Path: c.Path, State: c.State, PR: c.PR, URL: c.URL, Project: c.Project}
		switch {
		case c.State == "closed":
			cc.Title = cleanup.ClosedWarning(c.Forge, c.Branch, "the default branch")
			if c.Warning != "" {
				cc.Title = c.Warning
			}
		case !c.Eligible:
			cc.Title = c.Warning
			if cc.Title == "" {
				cc.Title = "PR merged, but this worktree is not ready to clean up"
			}
		case rst.Cleanup.Mode == config.CleanupAuto && !c.CleanAt.IsZero():
			cc.Title, cc.Ask = cleanupIn(c.CleanAt, now), "auto"
		default:
			cc.Title, cc.Ask = "PR merged — clean up?", "suggest"
		}
		cv.Candidates = append(cv.Candidates, cc)
	}
	v.Cleanup = cv
}

// reposJSON is GET /api/repos: the scan, byte for byte as `stackfork repos
// --json` prints it, with the live state of each repository under "live".
func (s *Server) reposJSON(res repos.Result, cfg *config.Config) []byte {
	b := repos.Marshal(res)
	if s.Live == nil {
		return b
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return b
	}
	views := s.liveViews(res.Repos, cfg)
	if lb, err := json.Marshal(views); err == nil {
		top["live"] = lb
	}
	nb, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return b
	}
	return append(nb, '\n')
}

// decorateCleanup puts the cleanup callout of each worktree on its
// instance, for the VersionCard.
func (s *Server) decorateCleanup(insts []inventory.Instance) []inventory.Instance {
	if s.Live == nil || len(insts) == 0 {
		return insts
	}
	s.repos.mu.Lock()
	cur := s.repos.res
	s.repos.mu.Unlock()
	if cur == nil { // collect runs inside the first scan: never start another
		return insts
	}
	cfg, _ := config.Load()
	byProject := map[string]*inventory.CleanupNote{}
	for repo, v := range s.liveViews(cur.Repos, cfg) {
		if v.Cleanup == nil {
			continue
		}
		for _, c := range v.Cleanup.Candidates {
			if c.Project != "" {
				byProject[c.Project] = c.note(repo)
			}
		}
	}
	if len(byProject) == 0 {
		return insts
	}
	out := append([]inventory.Instance(nil), insts...)
	for i := range out {
		if n := byProject[out[i].Project]; n != nil {
			out[i].Cleanup = n
		}
	}
	return out
}

// --- POST /api/live: on, off, resume ----------------------------------------

// consentText is the native dialog for turning live mode on (D12): it names
// the repository and its upstreams and says what happens to pushed commits.
func consentText(repo repos.Repo) string {
	var up []string
	seen := map[string]bool{}
	for _, w := range repo.Worktrees {
		if w.Upstream != "" && !seen[w.Upstream] {
			seen[w.Upstream] = true
			up = append(up, w.Upstream)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Turn on live mode for %s?\n\n", repo.Name)
	b.WriteString("While the dashboard runs, commits on the upstream of each worktree are fetched, fast-forwarded and built on this machine.\n")
	if len(up) > 0 {
		b.WriteString("\nUpstreams: " + strings.Join(up, ", ") + "\n")
	}
	b.WriteString("\nBranch cleanup is on suggest: it always asks first, unless you choose auto.")
	return b.String()
}

// handleLiveSwitch answers on, off and resume. It runs after the token,
// Origin and scan checks of handleLive.
func (s *Server) handleLiveSwitch(w http.ResponseWriter, repo *repos.Repo, op string) {
	if s.Live == nil {
		writeActionResult(w, http.StatusServiceUnavailable, actionResponse{Message: "live mode is not available in this build"})
		return
	}
	cmd := "stackfork live on " + shq(repo.Path)
	switch op {
	case "on":
		if err := s.Live.Prepare(repo.Path); err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error() + "; live mode is still off", Command: cmd})
			return
		}
		approved, err := Confirm("stackfork", consentText(*repo))
		if err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error() + "; live mode is still off. Run it in a terminal.", Command: cmd})
			return
		}
		if !approved {
			writeActionResult(w, http.StatusOK, actionResponse{Message: "cancelled; live mode is still off"})
			return
		}
		s.configMu.Lock()
		err = s.Live.Enable(repo.Path)
		s.configMu.Unlock()
		if err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error() + "; live mode is still off", Command: cmd})
			return
		}
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "live mode on for " + repo.Name})
	case "off":
		s.configMu.Lock()
		cfg, err := config.Load()
		if err == nil {
			cfg.UnsetLive(repo.Path)
			err = cfg.Save()
		}
		s.configMu.Unlock()
		if err != nil {
			writeActionResult(w, http.StatusInternalServerError, actionResponse{Message: err.Error()})
			return
		}
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "live mode off for " + repo.Name + "; a live run in progress finishes"})
	case "resume":
		if err := s.Live.Resume(repo.Path); err != nil {
			writeActionResult(w, http.StatusOK, actionResponse{Message: err.Error()})
			return
		}
		writeActionResult(w, http.StatusOK, actionResponse{OK: true, Message: "live mode resumed for " + repo.Name})
	}
}
