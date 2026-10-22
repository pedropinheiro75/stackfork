package live

import (
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// CleanupStatus is the branch-cleanup part of a repository's live status:
// what the dashboard (callouts and the `cleanup` selector) and `live status`
// show.
type CleanupStatus struct {
	Mode         string            `json:"mode"` // off, suggest or auto
	GraceHours   int               `json:"grace_hours"`
	DeleteBranch bool              `json:"delete_branch"`
	Candidates   []CandidateStatus `json:"candidates"`
	Fetch        string            `json:"fetch,omitempty"` // the failed-fetch note
	LastFetch    time.Time         `json:"last_fetch,omitzero"`
	Paused       bool              `json:"paused,omitempty"` // nothing is checked while the repository is paused
}

// CandidateStatus is one linked worktree whose branch is merged or closed.
// A kept branch is listed with Kept set: it is neither suggested nor cleaned.
type CandidateStatus struct {
	Branch  string `json:"branch"`
	Path    string `json:"path"`
	Head    string `json:"head"`
	Project string `json:"project,omitempty"` // the worktree's instance, if any
	State   string `json:"state"`             // merged or closed
	Via     string `json:"via,omitempty"`     // ancestry, cherry or patch-id
	PR      int    `json:"pr,omitempty"`
	URL     string `json:"url,omitempty"`
	Forge   string `json:"forge,omitempty"`
	// Eligible: a cleanup may start now. Warning says why not
	// ("PR merged, but this worktree has uncommitted changes").
	Eligible bool     `json:"eligible"`
	Warning  string   `json:"warning,omitempty"`
	Ignored  []string `json:"ignored,omitempty"` // ignored files that go with the worktree
	Kept     bool     `json:"kept,omitempty"`
	// Others are the branches of other instances prepared in the same
	// worktree. They are never downed, so they block the cleanup.
	Others []string `json:"other_instances,omitempty"`
	// Since is when the candidate first became eligible; CleanAt is Since plus
	// the grace period, set in auto mode only.
	Since   time.Time `json:"eligible_since,omitzero"`
	CleanAt time.Time `json:"clean_at,omitzero"`
}

// ScanInput is what one pass over a repository's candidates needs.
type ScanInput struct {
	Main, NewBase string
	Settings      config.Cleanup
	Targets       []Target // the repository's instances; a candidate's instance is the one at its path
	Now           time.Time
	GH            *cleanup.GH // nil: no enrichment (the one-shot commands never call out)
	// ActionLocked tells whether the dashboard's action lock is held for an instance.
	ActionLocked func(project string) bool
	// Persist records first-eligible times in the cleanup state; `live status`
	// only reads them.
	Persist bool
}

func adapterFiles(main string) []string {
	if k, _, err := knowledge.LoadFor(main, main); err == nil && k != nil {
		return k.AdapterFiles
	}
	return nil
}

// instance is the candidate's instance: same checkout and same branch (a
// project name is hash(root+branch), so one worktree can have several). The
// branches of the other instances in the same checkout come back as others.
func (in ScanInput) instance(path, branch string) (t *Target, others []string) {
	for i := range in.Targets {
		if !sameFile(in.Targets[i].Root, path) {
			continue
		}
		if in.Targets[i].Branch == branch {
			t = &in.Targets[i]
		} else {
			others = append(others, in.Targets[i].Branch)
		}
	}
	return t, others
}

// ScanCleanup lists the merged and closed candidates of the repository from
// its local refs (the fetch is the caller's), with their eligibility, keep
// state and grace countdown. It writes only the cleanup state, and only with
// Persist.
func ScanCleanup(in ScanInput) ([]CandidateStatus, error) {
	cands, err := cleanup.Candidates(in.Main)
	if err != nil {
		return nil, err
	}
	st, _ := LoadCleanupState(in.NewBase, in.Main)
	adapter := adapterFiles(in.Main)
	out := []CandidateStatus{}
	var keep []EligibleEntry
	for i, res := range cleanup.Detect(in.Main, cands) {
		c := cands[i]
		if res.State == cleanup.Open {
			continue
		}
		if in.GH != nil {
			res = in.GH.Enrich(in.Main, c.Head, res)
		}
		cs := CandidateStatus{Branch: c.Branch, Path: c.Path, Head: c.Head, State: res.State, Via: res.Via, PR: res.PR, URL: res.URL, Forge: res.Forge,
			Kept: st.Kept(c.Branch, c.Head)}
		job := CleanupJob{Main: in.Main, NewBase: in.NewBase, Candidate: c, Result: res, Settings: in.Settings, Adapter: adapter,
			Auto: in.Settings.Mode == config.CleanupAuto, ActionLocked: in.ActionLocked}
		t, others := in.instance(c.Path, c.Branch)
		if t != nil {
			cs.Project, job.Instance = t.Project, t
		}
		cs.Others, job.Others = others, others
		v := cleanup.Evaluate(job.CheckFor())
		cs.Eligible, cs.Warning, cs.Ignored = v.Eligible, v.Warning, v.Ignored
		if v.State == cleanup.Merged && v.Eligible && !cs.Kept {
			since, ok := st.since(c.Branch, c.Head)
			if !ok && in.Persist { // only the loop starts a grace period
				since, ok = in.Now.UTC(), true
			}
			if ok {
				cs.Since = since
				keep = append(keep, EligibleEntry{Branch: c.Branch, Head: c.Head, Since: since})
				if in.Settings.Mode == config.CleanupAuto {
					cs.CleanAt = since.Add(time.Duration(in.Settings.GraceHours) * time.Hour)
				}
			}
		}
		out = append(out, cs)
	}
	if in.Persist {
		UpdateCleanupState(in.NewBase, in.Main, func(s *CleanupState) bool {
			if sameEligible(s.Eligible, keep) {
				return false
			}
			s.Eligible = keep
			return true
		})
	}
	return out, nil
}

func sameEligible(a, b []EligibleEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Branch != b[i].Branch || a[i].Head != b[i].Head || !a[i].Since.Equal(b[i].Since) {
			return false
		}
	}
	return true
}

// Job builds the cleanup job for one scanned candidate. auto says whether it
// starts without a click (ignored files then block it).
func (in ScanInput) Job(c CandidateStatus, auto bool) CleanupJob {
	j := CleanupJob{Main: in.Main, NewBase: in.NewBase, Settings: in.Settings, Auto: auto, Adapter: adapterFiles(in.Main), ActionLocked: in.ActionLocked,
		Candidate: cleanup.Candidate{Path: c.Path, Branch: c.Branch, Head: c.Head},
		Result:    cleanup.Result{Branch: c.Branch, State: c.State, Via: c.Via, PR: c.PR, URL: c.URL, Forge: c.Forge}}
	j.Instance, j.Others = in.instance(c.Path, c.Branch)
	return j
}

// InspectCleanup is the one-shot look for `live status`: local refs only, no
// fetch and no gh.
func InspectCleanup(newBase, main string, cl config.Cleanup, ts []Target, now time.Time) *CleanupStatus {
	out := &CleanupStatus{Mode: cl.Mode, GraceHours: cl.GraceHours, DeleteBranch: cl.DeleteBranch, Candidates: []CandidateStatus{}}
	if cl.Mode == config.CleanupOff {
		return out
	}
	if cs, err := ScanCleanup(ScanInput{Main: main, NewBase: newBase, Settings: cl, Targets: ts, Now: now}); err == nil {
		out.Candidates = cs
	}
	return out
}
