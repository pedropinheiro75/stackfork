package live

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// Job is what one live run does (design D6). BuildJob picks the actions from
// the changed categories; Plan cuts the steps out of the full plan; the CLI
// runs that plan through StartEnv, so a live run shares every code path of a
// Start (secrets step, prepare, UpAndSeed, exec) and adds none.
type Job struct {
	Mode    string
	Compose bool     // prepare, then the plan's compose-up steps
	Migrate bool     // the plan's `exec once: schema` steps
	Rebuild []string // services rebuilt in this run, sorted; only services the mode starts
	Joined  bool     // Rebuild joins the plan's compose-up steps (compose also changed)
	// text is the row text of the rebuild: "up --build web (bun.lock changed)".
	text    string
	Cause   runs.Cause
	Notes   []string // what this run does not do, for the live state
	Covered []Change // every change this reconcile settles; the baseline moves for them after a success
}

// BuildJob maps the pending changes of a target to a Job. started says
// whether the mode starts a service (an excluded app, or a service that
// depends on one, is not rebuilt: its dependencies are the host's business).
func BuildJob(plan knowledge.Plan, mode string, changes []Change, started func(service string) bool) Job {
	j := Job{Mode: mode, Covered: changes}
	run, _ := plan.ForMode(mode)
	var files = map[string][]string{} // service -> changed paths, in order
	var cc []runs.CauseChange
	for _, c := range changes {
		cc = append(cc, runs.CauseChange{Path: c.Path, Category: c.Category, Service: c.Service})
		switch c.Category {
		case CatCompose:
			j.Compose = true
		case CatMigrations:
			j.Migrate = true
		case CatBuild:
			files[c.Service] = append(files[c.Service], c.Path)
		}
	}
	j.Cause.SetChanged(cc)
	var svcs []string
	for s := range files {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	var paths []string
	for _, s := range svcs {
		if started == nil || started(s) {
			j.Rebuild = append(j.Rebuild, s)
			for _, p := range files[s] {
				if !slices.Contains(paths, p) {
					paths = append(paths, p)
				}
			}
			continue
		}
		j.Notes = append(j.Notes, fmt.Sprintf("%s is not rebuilt: %s mode does not start it, so its dependencies changed on the host", s, mode))
	}
	if j.Migrate && len(schemaSteps(run)) == 0 {
		j.Migrate = false
		j.Notes = append(j.Notes, "migration files changed; this plan has no schema step")
	}
	if len(j.Rebuild) > 0 {
		shown := paths
		if len(shown) > 3 {
			shown = append(slices.Clone(shown[:3]), fmt.Sprintf("+%d more", len(paths)-3))
		}
		j.text = fmt.Sprintf("up --build %s (%s changed)", strings.Join(j.Rebuild, ", "), strings.Join(shown, ", "))
		j.Joined = j.Compose
	}
	return j
}

func schemaSteps(run []knowledge.Step) []knowledge.Step {
	var out []knowledge.Step
	for _, s := range run {
		if s.Kind == knowledge.KindExec && s.Once == knowledge.OnceSchema {
			out = append(out, s)
		}
	}
	return out
}

// Plan is the steps of full this job runs, in the order of design D10: the
// plan's prepare and compose-up steps for `compose`, one `rebuild` up for
// `build` alone, the data step beside any up (the seed keeps its rule), then
// the schema steps. Never a host or note step. Hand it to a PlanRunner with
// Exact set.
func (j Job) Plan(full knowledge.Plan) knowledge.Plan {
	out := full
	out.Steps = nil
	run, _ := full.ForMode(j.Mode)
	var up []knowledge.Step
	switch {
	case j.Compose:
		hasPrepare := false
		for _, s := range run {
			if s.Kind == knowledge.KindPrepare {
				out.Steps, hasPrepare = append(out.Steps, s), true
			}
		}
		if !hasPrepare { // prepare reuses the files the instance recorded
			out.Steps = append(out.Steps, knowledge.Step{ID: "prepare", Kind: knowledge.KindPrepare})
		}
		for _, s := range run {
			if s.Kind == knowledge.KindComposeUp {
				if j.Joined {
					s.Text = strings.Replace(j.text, "up ", "compose up ", 1)
				}
				up = append(up, s)
			}
		}
		if len(up) == 0 {
			up = []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}}
		}
	case len(j.Rebuild) > 0:
		up = []knowledge.Step{{ID: "rebuild", Kind: knowledge.KindComposeUp, Text: j.text}}
	}
	out.Steps = append(out.Steps, up...)
	if len(up) > 0 {
		for _, s := range run {
			if s.Kind == knowledge.KindExec && s.Once == knowledge.OnceData {
				out.Steps = append(out.Steps, s)
			}
		}
	}
	if j.Migrate {
		out.Steps = append(out.Steps, schemaSteps(run)...)
	}
	return out
}

// Empty is true when the job has nothing to run: the changes only had notes
// or a plan edit. No run is recorded for it.
func (j Job) Empty(full knowledge.Plan) bool { return len(j.Plan(full).Steps) == 0 }

// UpArgs are the build flag and the service list for the live run's `up`.
// services is what the compose-up step starts (nil: all).
func (j Job) UpArgs(services []string) (build bool, out []string) {
	switch {
	case len(j.Rebuild) == 0:
		return false, services
	case j.Joined:
		return true, services
	}
	return true, j.Rebuild
}
