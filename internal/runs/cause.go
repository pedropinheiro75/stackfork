package runs

// MaxCauseChanged caps Cause.Changed; the rest is counted in Cause.More
// (live-mode D10).
const MaxCauseChanged = 50

// Cause is why a live run started. Other runs have none.
type Cause struct {
	Upstream    *CauseUpstream `json:"upstream,omitempty"`
	Changed     []CauseChange  `json:"changed,omitempty"`
	More        int            `json:"more,omitempty"` // changed paths beyond MaxCauseChanged
	PlanChanged []string       `json:"plan_changed,omitempty"`
	Cleanup     *CauseCleanup  `json:"cleanup,omitempty"` // a branch cleanup run (live-mode D19)
}

// CauseCleanup names the merged branch a cleanup run removes.
type CauseCleanup struct {
	Branch string `json:"branch"`
	Result string `json:"result"`        // merged
	PR     int    `json:"pr,omitempty"`  // when gh knew it
	Via    string `json:"via,omitempty"` // git, or git+gh
}

// CauseUpstream is the fast-forward that brought the change.
type CauseUpstream struct {
	Ref     string `json:"ref"`
	From    string `json:"from"`
	To      string `json:"to"`
	Commits int    `json:"commits"`
}

// CauseChange is one changed watched path.
type CauseChange struct {
	Path     string `json:"path"`
	Category string `json:"category"`
	Service  string `json:"service,omitempty"`
}

// SetChanged stores the changed paths, at most MaxCauseChanged, and counts the rest.
func (c *Cause) SetChanged(all []CauseChange) {
	c.Changed, c.More = all, 0
	if len(all) > MaxCauseChanged {
		c.Changed, c.More = all[:MaxCauseChanged], len(all)-MaxCauseChanged
	}
}
