package learn

import (
	"fmt"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// StepWarnings lists what is wrong with a recorded exec step (a warning,
// never a refusal): it repeats a migrate or seed the app's own start already
// runs, or a seed that starts with the migrate command of an earlier schema
// step (the migrate would run twice).
func StepWarnings(k *knowledge.Knowledge, s knowledge.Step) []string {
	if k == nil || s.Kind != knowledge.KindExec || (s.Once != knowledge.OnceSchema && s.Once != knowledge.OnceData) {
		return nil
	}
	var out []string
	remove := fmt.Sprintf("stackfork learn record step --remove %s", s.ID)
	if !s.IsHostExec() {
		name := s.Service
		if name == "" {
			name = appService(k)
		}
		if svc := svcNamed(k.Facts.Services, name); svc != nil {
			if r := svc.StartupMigrate; r != nil && (s.Once == knowledge.OnceSchema || startupMigratePattern.MatchString(s.Cmd)) {
				out = append(out, fmt.Sprintf("step %s runs a migrate, but %s already migrates on every start (%s). Both would run on Start. Fix: %s, and say so in a note", s.ID, name, r.Where(), remove))
			}
			if r := svc.StartupSeed; r != nil && (s.Once == knowledge.OnceData || startupSeedPattern.MatchString(s.Cmd)) {
				out = append(out, fmt.Sprintf("step %s runs a seed, but %s already seeds on every start (%s). It may duplicate data. Fix: %s, or record `learn record step --id %s --kind exec --once none` and say so in a note", s.ID, name, r.Where(), remove, s.ID))
			}
		}
	}
	if s.Once == knowledge.OnceData {
		for _, m := range k.Plan.Steps {
			if m.ID != s.ID && m.Kind == knowledge.KindExec && m.Once == knowledge.OnceSchema && m.Cmd != "" &&
				m.IsHostExec() == s.IsHostExec() && strings.HasPrefix(strings.TrimSpace(s.Cmd), m.Cmd) {
				out = append(out, fmt.Sprintf("step %s starts with the command of migrate step %s, so the migrate runs twice. Fix: record the seed alone, or remove the migrate step with `stackfork learn record step --remove %s`", s.ID, m.ID, m.ID))
				break
			}
		}
	}
	return out
}

// PlanWarnings is StepWarnings for every step of the plan.
func PlanWarnings(k *knowledge.Knowledge) []string {
	var out []string
	for _, s := range k.Plan.Steps {
		out = append(out, StepWarnings(k, s)...)
	}
	return out
}
