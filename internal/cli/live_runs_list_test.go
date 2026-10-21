package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// `stackfork runs` lists a live run with command live and trigger live, and
// `runs show --json` carries its cause.
func TestRunsListsLiveRun(t *testing.T) {
	dir := t.TempDir()
	c := &runs.Cause{Upstream: &runs.CauseUpstream{Ref: "origin/feature-login", From: "3f9a2c1", To: "8b07d44", Commits: 3}}
	c.SetChanged([]runs.CauseChange{{Path: "bun.lock", Category: "build", Service: "web"}})
	r, err := runs.Begin(dir, runs.Meta{Command: "live", Trigger: "live", Mode: "container", Cause: c, NoWait: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Step("secrets", "secrets", "resolve secrets").Skip("no secrets")
	s := r.Step("rebuild", "compose-up", "up --build web")
	s.Start()
	s.End(errors.New("build failed"), 1)
	r.Finish(errors.New("build failed"))

	out, _, code := runsCmd(t, dir)
	f := strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[1])
	if code != 0 || len(f) < 6 || f[1] != "live" || f[2] != "live" || f[4] != "failed" || f[len(f)-1] != "rebuild" {
		t.Fatalf("%d %q", code, out)
	}
	if out, _, _ := runsCmd(t, dir, "show", "--json"); !strings.Contains(out, `"commits": 3`) || !strings.Contains(out, `"bun.lock"`) {
		t.Fatalf("cause missing:\n%s", out)
	}
}
