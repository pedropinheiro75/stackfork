package runs

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCauseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := &Cause{Upstream: &CauseUpstream{Ref: "origin/feature-login", From: "3f9a2c1", To: "8b07d44", Commits: 3}, PlanChanged: []string{"migrate"}}
	c.SetChanged([]CauseChange{{Path: "bun.lock", Category: "build", Service: "web"}, {Path: "db/m.sql", Category: "migrations"}})
	r, err := Begin(dir, Meta{Command: "live", Trigger: "live", Mode: "container", Cause: c})
	if err != nil {
		t.Fatal(err)
	}
	r.Finish(nil)
	got, err := Get(dir, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != "live" || got.Trigger != "live" || got.Cause == nil || got.Cause.Upstream.Commits != 3 ||
		len(got.Cause.Changed) != 2 || got.Cause.Changed[0].Service != "web" || got.Cause.PlanChanged[0] != "migrate" || got.Cause.More != 0 {
		t.Fatalf("%+v %+v", got, got.Cause)
	}
	plain, _ := Begin(dir, Meta{Command: "up", Trigger: "cli"})
	plain.Finish(nil)
	if g, _ := Get(dir, plain.ID); g.Cause != nil {
		t.Fatalf("a run without a cause stores %+v", g.Cause)
	}
}

func TestCauseCapsChangedAtFifty(t *testing.T) {
	var all []CauseChange
	for i := 0; i < 73; i++ {
		all = append(all, CauseChange{Path: fmt.Sprintf("m/%03d.sql", i), Category: "migrations"})
	}
	var c Cause
	c.SetChanged(all)
	if len(c.Changed) != MaxCauseChanged || c.More != 23 || c.Changed[0].Path != "m/000.sql" {
		t.Fatalf("%d changed, more %d", len(c.Changed), c.More)
	}
}

func TestNoWaitBeginFailsAtOnce(t *testing.T) {
	dir := t.TempDir()
	held, err := Begin(dir, Meta{Command: "start-env", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Finish(nil)
	start := time.Now()
	_, err = Begin(dir, Meta{Command: "live", Trigger: "live", NoWait: true})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("a no-wait Begin took %s", d)
	}
	if ids := listIDs(dir); len(ids) != 1 {
		t.Fatalf("the refused Begin wrote a run: %v", ids)
	}
}
