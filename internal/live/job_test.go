package live

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

var jobPlan = knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
	{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", "docker-compose.yml"}},
	{ID: "up", Kind: knowledge.KindComposeUp, Modes: []string{"container"}},
	{ID: "up-deps", Kind: knowledge.KindComposeUp, Modes: []string{"hybrid"}, Exclude: []string{"web"}},
	{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "db", Cmd: "bun run db:migrate"},
	{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "db", Cmd: "s"},
	{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "app", Cmd: "bun dev"},
	{ID: "hint", Kind: knowledge.KindNote, Text: "open the app"},
}}

func ids(p knowledge.Plan) string {
	var l []string
	for _, s := range p.Steps {
		l = append(l, s.ID)
	}
	return strings.Join(l, " ")
}

func ch(path, cat, svc string) Change { return Change{Path: path, Category: cat, Service: svc} }

func TestBuildJobPerCategory(t *testing.T) {
	all := func(string) bool { return true }
	cases := []struct {
		name    string
		changes []Change
		mode    string
		started func(string) bool
		steps   string
		rebuild []string
		joined  bool
	}{
		{"compose", []Change{ch("docker-compose.yml", CatCompose, "")}, "container", all, "prepare up seed", nil, false},
		{"build", []Change{ch("bun.lock", CatBuild, "web")}, "container", all, "rebuild seed", []string{"web"}, false},
		{"build and compose join one up", []Change{ch("bun.lock", CatBuild, "web"), ch("docker-compose.yml", CatCompose, "")}, "container", all, "prepare up seed", []string{"web"}, true},
		{"migrations", []Change{ch("prisma/migrations/1/m.sql", CatMigrations, "")}, "container", all, "migrate", nil, false},
		{"all three, in the fixed order", []Change{ch("prisma/migrations/1/m.sql", CatMigrations, ""), ch("bun.lock", CatBuild, "web"), ch("Dockerfile", CatBuild, "web")}, "container", all, "rebuild seed migrate", []string{"web"}, false},
		{"plan alone runs nothing", []Change{ch(".stackfork/knowledge.json", CatPlan, "")}, "container", all, "", nil, false},
		{"hybrid never lists the host step", []Change{ch("docker-compose.yml", CatCompose, "")}, "hybrid", all, "prepare up-deps seed", nil, false},
		{"excluded service is not rebuilt", []Change{ch("bun.lock", CatBuild, "web")}, "hybrid", func(s string) bool { return s != "web" }, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := BuildJob(jobPlan, c.mode, c.changes, c.started)
			if got := ids(j.Plan(jobPlan)); got != c.steps {
				t.Errorf("steps %q, want %q", got, c.steps)
			}
			if !reflect.DeepEqual(j.Rebuild, c.rebuild) || j.Joined != c.joined {
				t.Errorf("rebuild %v joined %v", j.Rebuild, j.Joined)
			}
			for _, s := range j.Plan(jobPlan).Steps {
				if s.Kind == knowledge.KindHost || s.Kind == knowledge.KindNote {
					t.Errorf("a live run lists %s step %s", s.Kind, s.ID)
				}
			}
		})
	}
}

func TestBuildJobNotes(t *testing.T) {
	j := BuildJob(jobPlan, "hybrid", []Change{ch("bun.lock", CatBuild, "web")}, func(s string) bool { return s != "web" })
	if !j.Empty(jobPlan) || len(j.Notes) != 1 || !strings.Contains(j.Notes[0], "web") || !strings.Contains(j.Notes[0], "host") {
		t.Fatalf("%v", j.Notes)
	}
	noSchema := knowledge.Plan{Steps: []knowledge.Step{{ID: "up", Kind: knowledge.KindComposeUp}}}
	j = BuildJob(noSchema, "container", []Change{ch("db/migrate/1.rb", CatMigrations, "")}, func(string) bool { return true })
	if !j.Empty(noSchema) || len(j.Notes) != 1 || !strings.Contains(j.Notes[0], "no schema step") {
		t.Fatalf("%v", j.Notes)
	}
}

func TestBuildJobRebuildStepNamesWhatItBuilds(t *testing.T) {
	j := BuildJob(jobPlan, "container", []Change{ch("bun.lock", CatBuild, "web"), ch("Dockerfile", CatBuild, "worker")}, func(string) bool { return true })
	p := j.Plan(jobPlan)
	if p.Steps[0].ID != "rebuild" || p.Steps[0].Kind != knowledge.KindComposeUp || p.Steps[0].Text != "up --build web, worker (bun.lock, Dockerfile changed)" {
		t.Fatalf("%+v", p.Steps[0])
	}
}

func TestJobUpArgs(t *testing.T) {
	j := Job{Rebuild: []string{"web"}}
	if b, s := j.UpArgs(nil); !b || !reflect.DeepEqual(s, []string{"web"}) {
		t.Fatal(b, s)
	}
	j.Joined = true
	if b, s := j.UpArgs([]string{"db", "web"}); !b || !reflect.DeepEqual(s, []string{"db", "web"}) {
		t.Fatal(b, s)
	}
	if b, s := (Job{}).UpArgs([]string{"db"}); b || !reflect.DeepEqual(s, []string{"db"}) {
		t.Fatal(b, s)
	}
}

func TestJobCauseCoversChanges(t *testing.T) {
	changes := []Change{ch("bun.lock", CatBuild, "web"), ch("m.sql", CatMigrations, "")}
	j := BuildJob(jobPlan, "container", changes, func(string) bool { return true })
	if len(j.Cause.Changed) != 2 || j.Cause.Changed[0].Service != "web" || len(j.Covered) != 2 {
		t.Fatalf("%+v", j.Cause)
	}
}
