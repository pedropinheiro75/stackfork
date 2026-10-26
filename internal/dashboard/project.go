package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// projectJSON is GET /api/project (docs/design/project-page/api.md).
type projectJSON struct {
	Name          string            `json:"name"`
	Path          string            `json:"path"`
	DefaultBranch string            `json:"default_branch"`
	DefaultMode   string            `json:"default_mode"`
	Modes         []string          `json:"modes"`
	Knowledge     projectKnowledge  `json:"knowledge"`
	Live          projectLive       `json:"live"`
	Entry         projectEntry      `json:"entry"`
	Checkouts     []projectCheckout `json:"checkouts"`
	Branches      []projectBranch   `json:"branches_not_checked_out"`
	Workflow      map[string]any    `json:"workflow"`
	Config        projectConfigJSON `json:"config"`
}

type projectKnowledge struct {
	State      string `json:"state"` // verified, draft, borrowed, none
	From       string `json:"from,omitempty"`
	VerifiedAt string `json:"verified_at,omitempty"`
}

type projectLive struct {
	On     bool   `json:"on"`
	State  string `json:"state"` // off, on, paused
	Reason string `json:"reason"`
}

type projectEntry struct {
	State  string `json:"state"` // recorded, guessed, none
	Target string `json:"target"`
	URL    string `json:"url"`
}

type projectBranch struct {
	Branch   string `json:"branch"`
	Upstream string `json:"upstream"`
}

type projectCheckout struct {
	Key      string          `json:"key"`
	Branch   string          `json:"branch"`
	Path     string          `json:"path"`
	Main     bool            `json:"main"`
	Changes  bool            `json:"changes"`
	Ahead    int             `json:"ahead"`
	Behind   int             `json:"behind"`
	Upstream string          `json:"upstream"`
	Mode     string          `json:"mode"`
	State    string          `json:"state"` // running, partial, starting, stopped, no-stack
	Project  string          `json:"project"`
	OpenURL  string          `json:"open_url"`
	LastRun  *projectLastRun `json:"last_run"`
}

type projectLastRun struct {
	ID         int                    `json:"id"`
	Command    string                 `json:"command"`
	Outcome    string                 `json:"outcome"`
	Mode       string                 `json:"mode"`
	FailedStep string                 `json:"failed_step"`
	StartedAt  time.Time              `json:"started_at"`
	DurationMS int64                  `json:"duration_ms"`
	Steps      map[string]projectStep `json:"steps"`
}

type projectStep struct {
	State  string `json:"state"`
	MS     int64  `json:"ms"`
	Reason string `json:"reason"`
}

type projectConfigJSON struct {
	Secrets       []projectSecret `json:"secrets"`
	EnvOverrides  []projectEnv    `json:"env_overrides"`
	BranchCleanup string          `json:"branch_cleanup"`
}

type projectSecret struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	From      string `json:"from"`
	Hint      string `json:"hint"`
}

type projectEnv struct {
	Service  string `json:"service"`
	Name     string `json:"name"`
	Value    string `json:"value"` // empty when masked
	Masked   bool   `json:"masked"`
	Checkout string `json:"checkout"`
}

func projectError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck // client gone
}

// handleProject is the Project page's read model (docs/design/project-page/api.md):
// one response from the scan, inventory, runs, knowledge and config the
// dashboard already reads. Read-only.
func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	name, path := r.URL.Query().Get("name"), r.URL.Query().Get("path")
	res, _ := s.currentRepos()
	var repo *repos.Repo
	for i := range res.Repos {
		if res.Repos[i].Name == name && name != "" && (path == "" || res.Repos[i].Path == path) {
			if repo != nil {
				projectError(w, http.StatusConflict, fmt.Sprintf("more than one repository is named %q; pass path=", name))
				return
			}
			repo = &res.Repos[i]
		}
	}
	if repo == nil {
		projectError(w, http.StatusNotFound, fmt.Sprintf("no repository named %q", name))
		return
	}
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
	}
	out := s.projectFor(*repo, cfg)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out) //nolint:errcheck // client gone
}

func (s *Server) projectFor(repo repos.Repo, cfg *config.Config) projectJSON {
	out := projectJSON{Name: repo.Name, Path: repo.Path, DefaultBranch: repo.DefaultBranch, Modes: []string{},
		Checkouts: []projectCheckout{}, Branches: []projectBranch{}, Workflow: map[string]any{},
		Config: projectConfigJSON{Secrets: []projectSecret{}, EnvOverrides: []projectEnv{}, BranchCleanup: string(cfg.CleanupFor(repo.Path).Mode)}}

	k, from, kerr := knowledge.LoadFor(repo.Path, repo.Path)
	if kerr != nil {
		k = nil
	}
	out.Knowledge = projectKnowledge{State: "none"}
	if k != nil {
		out.Knowledge.State = knowledge.StatusDraft
		if k.Status == knowledge.StatusVerified {
			out.Knowledge.State = knowledge.StatusVerified
		}
		if k.Verification != nil {
			out.Knowledge.VerifiedAt = k.Verification.Date
		}
		if from != repo.Path {
			out.Knowledge.State, out.Knowledge.From = "borrowed", filepath.Base(from)
		}
	}

	plan, derived := knowledge.Plan{}, true
	if k != nil && len(k.Plan.Steps) > 0 {
		plan, derived = k.Plan, false
	} else if d, ok := s.derivedSteps(repo); ok {
		for _, st := range d.Steps {
			plan.Steps = append(plan.Steps, st.Step)
		}
	}
	for _, m := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
		if plan.SupportsMode(m) {
			out.Modes = append(out.Modes, m)
		}
	}
	out.DefaultMode, _ = cfg.ModeForPlan(repo.Path, plan)

	var insts []inventory.Instance
	if inv, err := s.collect(); err == nil {
		insts = inv.Instances
	}
	byProject := map[string]inventory.Instance{}
	for _, i := range insts {
		byProject[i.Project] = i
	}

	// entry: the recorded or guessed target, and where Open App goes for the main checkout
	out.Entry = projectEntry{State: "none"}
	if e := repoEntry(repo); e != nil {
		out.Entry.State, out.Entry.Target = e.State, e.Target
	}

	wts := append([]repos.Worktree{}, repo.Worktrees...)
	sort.SliceStable(wts, func(i, j int) bool {
		if wts[i].Main != wts[j].Main {
			return wts[i].Main
		}
		return wts[i].Branch < wts[j].Branch
	})
	for _, wt := range wts {
		c := projectCheckout{Key: wt.Branch, Branch: wt.Branch, Path: wt.Path, Main: wt.Main, Changes: wt.Dirty,
			Ahead: wt.Ahead, Behind: wt.Behind, Upstream: wt.Upstream, Mode: out.DefaultMode, State: "no-stack"}
		if c.Main {
			c.Key = "main"
		}
		if wt.Instance != nil {
			c.Project, c.State = wt.Instance.Project, projectState(wt.Instance.State)
			inst, haveInst := byProject[c.Project]
			if haveInst {
				c.State = projectState(inst.State)
				if a := openApp(inst.Apps, inst.EntryNone); a != nil {
					c.OpenURL = a.URL
				}
			}
			if dir, _, _, _, err := s.resolveInstance(c.Project); err == nil {
				if run := runs.Latest(dir); run != nil {
					c.LastRun = lastRun(run)
					if c.LastRun.Mode != "" {
						c.Mode = c.LastRun.Mode
					}
					if run.Outcome == runs.Running {
						c.State = "starting"
					}
				}
				out.Config.EnvOverrides = append(out.Config.EnvOverrides, envOverrides(dir, c.Key)...)
			}
		}
		if c.Main {
			out.Entry.URL = c.OpenURL
		}
		out.Checkouts = append(out.Checkouts, c)
	}
	for _, b := range repo.Branches {
		up := "none"
		if b.Upstream != "" {
			up = fmt.Sprintf("ahead %d behind %d", b.Ahead, b.Behind)
		}
		out.Branches = append(out.Branches, projectBranch{Branch: b.Name, Upstream: up})
	}

	out.Live = projectLive{State: "off"}
	if v, ok := s.liveViews([]repos.Repo{repo}, cfg)[repo.Path]; ok && v.Enabled {
		out.Live = projectLive{On: true, State: "on", Reason: v.Reason}
		if v.State == "paused" {
			out.Live.State = "paused"
		}
	}

	if rows, err := s.secretRows(repo.Path); err == nil {
		for _, r := range rows {
			out.Config.Secrets = append(out.Config.Secrets, projectSecret{Name: r.Name, Reference: r.Reference, Status: r.Status, Source: r.Source, From: r.From, Hint: r.Hint})
		}
	}

	opts := lifecycle.GraphOpts{Root: repo.Path, Main: repo.Path, K: k, Derived: derived, StateBase: s.NewStateBase,
		HasSecrets: hasSecretNames(repo.Path, cfg), EntryTarget: out.Entry.Target, EntryURL: out.Entry.URL}
	for _, m := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
		out.Workflow[m] = lifecycle.PlanGraph(plan, m, opts)
	}
	return out
}

// projectState maps an inventory state to the page's checkout states.
func projectState(s string) string {
	switch s {
	case inventory.StateRunning, inventory.StatePartial:
		return s
	}
	return "stopped"
}

func lastRun(r *runs.Run) *projectLastRun {
	l := &projectLastRun{ID: r.ID, Command: r.Command, Outcome: r.Outcome, Mode: r.Mode, FailedStep: r.FailedStep(),
		StartedAt: r.StartedAt, DurationMS: r.DurationMS, Steps: map[string]projectStep{}}
	for _, st := range r.Steps {
		l.Steps[st.ID] = projectStep{State: st.State, MS: st.DurationMS, Reason: st.Reason}
	}
	return l
}

// envOverrides are the instance's overrides; a masked value never leaves
// lifecycle.EnvView, so none reaches the page.
func envOverrides(instanceDir, checkout string) []projectEnv {
	view, err := lifecycle.EnvView(instanceDir)
	if err != nil {
		return nil
	}
	var out []projectEnv
	for _, svc := range view {
		for _, v := range svc.Vars {
			if v.Origin != "override" {
				continue
			}
			e := projectEnv{Service: svc.Name, Name: v.Name, Masked: v.Masked, Checkout: checkout}
			if v.Value != nil && !v.Masked {
				e.Value = *v.Value
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// derivedSteps is what a Start of a repo with no recorded steps would run,
// from the same per-repo cache the Repos list uses.
func (s *Server) derivedSteps(repo repos.Repo) (lifecycle.DerivedPlan, bool) {
	if s.DerivePlan == nil || !startable(repo) {
		return lifecycle.DerivedPlan{}, false
	}
	s.repos.mu.Lock()
	e, ok := s.repos.derived[repo.Path]
	insts := s.repos.insts
	s.repos.mu.Unlock()
	if !ok {
		e.plan, e.err = s.DerivePlan(repo.Path, insts)
		s.repos.mu.Lock()
		if s.repos.derived == nil {
			s.repos.derived = map[string]derivedEntry{}
		}
		s.repos.derived[repo.Path] = e
		s.repos.mu.Unlock()
	}
	return e.plan, e.err == nil
}
