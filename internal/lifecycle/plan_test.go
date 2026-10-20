package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/learn"
)

// The scan's own hybrid proposal must start in both modes: its exec steps may
// not target the app service that hybrid leaves out.
func TestProposedHybridPlanPassesCheckInBothModes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"),
		[]byte(`{"scripts":{"dev":"nuxt dev","db:migrate":"x","db:seed":"y"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &knowledge.Knowledge{}
	k.Facts.Services = []knowledge.Service{
		{Name: "app", Build: true, Ports: []string{"3000:3000"}},
		{Name: "db", Ports: []string{"5432:5432"}},
	}
	k.Facts.Scripts = []knowledge.Script{{Name: "dev", Command: "nuxt dev", Source: "package.json"}}
	p := learn.ProposePlan(root, k)
	base := t.TempDir()
	if _, err := TrustHere(base, root, p); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
		if err := (PlanRunner{NewBase: base, Main: root, Mode: mode, Plan: p}).Check(); err != nil {
			t.Errorf("%s: %v", mode, err)
		}
	}
}

func TestCheckRefusesDataStepOnExcludedService(t *testing.T) {
	p := knowledge.Plan{AppService: "app", Steps: []knowledge.Step{
		{ID: "up", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}},
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Cmd: "s"},
	}}
	err := PlanRunner{NewBase: t.TempDir(), Main: t.TempDir(), Mode: knowledge.ModeContainer, Plan: p}.Check()
	if err == nil || !strings.Contains(err.Error(), "step seed runs in app") {
		t.Fatalf("%v", err)
	}
}
