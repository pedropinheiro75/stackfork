package live

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
)

// A worktree prepared on several branches has several instances with the same
// root. The candidate's instance is the one of its branch; the others are
// blockers, never downed.
func TestScanCleanupMatchesTheInstanceByBranchAndBlocksOnTheOthers(t *testing.T) {
	f := newCleanupFx(t)
	old := f.Inst
	old.Project, old.Branch, old.InstanceDir = "acme-web-old-branch", "old-branch", f.State+"/acme-web-old-branch"
	in := ScanInput{Main: f.Main, NewBase: f.State, Settings: config.Cleanup{Mode: config.CleanupAuto, GraceHours: 1}, Now: time.Now(),
		Targets: []Target{old, f.Inst}} // the old branch comes first in the inventory
	cs, err := ScanCleanup(in)
	if err != nil || len(cs) != 1 {
		t.Fatalf("%+v %v", cs, err)
	}
	c := cs[0]
	if c.Project != "acme-web-feature-login" {
		t.Errorf("the candidate took the instance %q", c.Project)
	}
	if c.Eligible || len(c.Others) != 1 || c.Others[0] != "old-branch" || !strings.Contains(c.Warning, "old-branch") {
		t.Errorf("blocker not reported: %+v", c)
	}
	if j := in.Job(c, true); j.Instance == nil || j.Instance.Project != "acme-web-feature-login" {
		t.Errorf("job instance: %+v", j.Instance)
	}
	// Even if a caller runs it, the re-check refuses: nothing is downed.
	downs := 0
	j := in.Job(c, true)
	j.Down = func(_ io.Writer) error { downs++; return nil }
	if out := RunCleanup(t.Context(), j); out.Err == nil || downs != 0 {
		t.Errorf("the cleanup ran: %+v downs=%d", out, downs)
	}
}

func TestScanCleanupWithoutOtherInstancesStaysEligible(t *testing.T) {
	f := newCleanupFx(t)
	in := ScanInput{Main: f.Main, NewBase: f.State, Settings: config.Cleanup{Mode: config.CleanupSuggest}, Now: time.Now(), Targets: []Target{f.Inst}}
	cs, _ := ScanCleanup(in)
	if len(cs) != 1 || !cs[0].Eligible || len(cs[0].Others) != 0 {
		t.Fatalf("%+v", cs)
	}
}
