package cli

import (
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

func TestLearnRecordStepAcceptsOnceDeps(t *testing.T) {
	root, stateDir, _ := templateEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir, "learn", "record", "step"}
	if _, stderr, code := runCLI(t, append(g, "--id", "install", "--kind", "exec", "--on", "host", "--once", "deps", "--cmd", "yarn install --frozen-lockfile", "--before", "up")...); code != 0 {
		t.Fatal(stderr)
	}
	k, err := knowledge.LoadFirst(root)
	if err != nil || k == nil {
		t.Fatal(err)
	}
	var got *knowledge.Step
	for i := range k.Plan.Steps {
		if k.Plan.Steps[i].ID == "install" {
			got = &k.Plan.Steps[i]
		}
	}
	if got == nil || got.Once != knowledge.OnceDeps || !got.IsHostExec() {
		t.Fatalf("%+v", got)
	}
	// part of the trusted text like any step
	with := knowledge.PlanHash(k.Plan.Steps)
	got.Once = ""
	if knowledge.PlanHash(k.Plan.Steps) == with {
		t.Fatal("once: deps must change the trust hash")
	}
	if _, stderr, code := runCLI(t, append(g, "--id", "n", "--kind", "note", "--once", "deps", "--text", "x")...); code == 0 || !strings.Contains(stderr, "once") {
		t.Fatalf("a note cannot be once: deps: %d %s", code, stderr)
	}
}
