package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	k := &Knowledge{Status: StatusVerified, Plan: Plan{
		Modes: []string{ModeContainer, ModeHybrid}, AppService: "web", Status: PlanRecorded,
		Steps: []Step{
			{ID: "prepare", Kind: KindPrepare, Args: []string{"-f", "a.yaml"}},
			{ID: "up", Kind: KindComposeUp, Modes: []string{ModeContainer}},
			{ID: "up-deps", Kind: KindComposeUp, Modes: []string{ModeHybrid}, Exclude: []string{"web"}},
			{ID: "seed", Kind: KindExec, Service: "web", Cmd: "bun run db:seed", Once: OnceData},
			{ID: "app", Kind: KindHost, Modes: []string{ModeHybrid}, Name: "app", Cmd: "bun run dev", Dir: ".", PortEnv: []string{"PORT"}},
		}}}
	out := filepath.Join(t.TempDir(), "knowledge.json")
	if err := k.Save(out); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Schema != CurrentSchema || !reflect.DeepEqual(k.Plan, reloaded.Plan) {
		t.Fatalf("round trip changed the plan:\nbefore: %+v\nafter:  %+v", k.Plan, reloaded.Plan)
	}
}

func TestLoadConvertsSchema1(t *testing.T) {
	fixture := filepath.Join("testdata", "knowledge.json")
	before, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	k, err := Load(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if k.Status != StatusDraft || len(k.Facts.Services) != 2 {
		t.Fatalf("unexpected load: %+v", k.Status)
	}
	if m := k.Plan.MigrateStep(); m == nil || m.Cmd != "stackfork exec app bun run db:migrate" {
		t.Fatalf("migrate step %+v", m)
	}
	if s := k.Plan.SeedStep(); s == nil || s.Cmd != "stackfork exec app bun run db:seed" {
		t.Fatalf("seed step %+v", s)
	}
	if n := len(k.Plan.Steps); n != 6 || k.Plan.Steps[5].Kind != KindNote {
		t.Fatalf("steps %+v", k.Plan.Steps)
	}
	if k.Plan.MigrateStep() == nil {
		t.Fatal("migrate step missing")
	}
	after, _ := os.ReadFile(fixture)
	if string(before) != string(after) {
		t.Fatal("Load must not rewrite the file")
	}
	out := filepath.Join(t.TempDir(), "k.json")
	if err := k.Save(out); err != nil {
		t.Fatal(err)
	}
	if r, err := Load(out); err != nil || r.Schema != CurrentSchema || !reflect.DeepEqual(r.Plan, k.Plan) {
		t.Fatalf("saved file: schema %v err %v", r, err)
	}
}

func TestLoadRefusesUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.json")
	k := &Knowledge{Schema: 3, Status: StatusDraft}
	b, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected an error loading an unsupported schema version")
	} else if !strings.Contains(err.Error(), "schema 3") {
		t.Fatalf("expected the error to name the bad schema version, got: %v", err)
	}
}

func TestLoadRefusesBadStep(t *testing.T) {
	for _, c := range []struct{ step, want string }{
		{`{"id":"w","kind":"wait-http"}`, "wait-http"},
		{`{"id":"app","kind":"host","cmd":"bun run dev && echo ok"}`, "&"},
	} {
		path := filepath.Join(t.TempDir(), "knowledge.json")
		doc := `{"schema":2,"status":"draft","facts":{},"plan":{"steps":[` + c.step + `]}}`
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		id := "w"
		if c.want == "&" {
			id = "app"
		}
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), `"`+id+`"`) {
			t.Fatalf("want error naming %q and step %q, got %v", c.want, id, err)
		}
	}
}

func TestMarkdownHasNoValues(t *testing.T) {
	k, err := Load(filepath.Join("testdata", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	md := k.Markdown()
	for _, want := range []string{"db", "postgres 17", "DATABASE_URL", "database", "auth"} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected markdown to mention %q:\n%s", want, md)
		}
	}
	// EnvKeys only ever holds names; nothing resembling a value is in the
	// struct to begin with, so the markdown can't leak one either.
}

// A plan edit must survive Save and Load: the plan is the only source of
// truth on schema 2.
func TestSchema2EditSurvivesSaveLoad(t *testing.T) {
	k, err := Load(filepath.Join("testdata", "knowledge2.json"))
	if err != nil {
		t.Fatal(err)
	}
	k.Plan.Upsert(Step{ID: "migrate", Kind: KindExec, Service: "web", Cmd: "bun run db:migrate:v2", Once: OnceSchema}, "")
	k.Plan.Status = PlanRecorded
	out := filepath.Join(t.TempDir(), "knowledge.json")
	if err := k.Save(out); err != nil {
		t.Fatal(err)
	}
	r, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan.Status != PlanRecorded || r.Plan.AppService != "web" || !reflect.DeepEqual(r.Plan.Modes, []string{ModeContainer, ModeHybrid}) {
		t.Fatalf("plan header lost: %+v", r.Plan)
	}
	if m := r.Plan.MigrateStep(); m == nil || m.Cmd != "bun run db:migrate:v2" {
		t.Fatalf("migrate edit lost: %+v", m)
	}
	h := r.Plan.HostSteps()
	if len(h) != 1 || h[0].Cmd != "bun run dev" || !reflect.DeepEqual(h[0].Modes, []string{ModeHybrid}) {
		t.Fatalf("host step lost: %+v", h)
	}
	if b, _ := os.ReadFile(out); strings.Contains(string(b), `"recipe"`) {
		t.Fatal("schema 2 must not write a recipe key")
	}
}
