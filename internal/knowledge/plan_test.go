package knowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitArgvQuotesAndRefusals(t *testing.T) {
	got, err := SplitArgv(`"./scripts/dev server.sh" --port 3000`)
	if err != nil || !reflect.DeepEqual(got, []string{"./scripts/dev server.sh", "--port", "3000"}) {
		t.Fatalf("got %q err %v", got, err)
	}
	for _, c := range []string{"$", "`", ";", "|", "&", "<", ">", "\n"} {
		_, err := SplitArgv("bun run dev " + c + "X")
		if err == nil || !strings.Contains(err.Error(), c) || !strings.Contains(err.Error(), "host step") {
			t.Fatalf("char %q: err %v", c, err)
		}
	}
}

func TestFromRecipeOrder(t *testing.T) {
	p := FromRecipe(Recipe{Prepare: "stackfork prepare -f a.yaml", Migrate: "m", SeedOnce: "s", SeedService: "web", Login: []string{"l"}})
	var kinds []string
	for _, s := range p.Steps {
		kinds = append(kinds, s.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{KindPrepare, KindComposeUp, KindExec, KindExec, KindNote}) {
		t.Fatalf("kinds %v", kinds)
	}
	if !reflect.DeepEqual(p.Steps[0].Args, []string{"-f", "a.yaml"}) {
		t.Fatalf("args %v", p.Steps[0].Args)
	}
	if m := p.MigrateStep(); m == nil || m.Cmd != "m" {
		t.Fatalf("migrate %+v", m)
	}
	if s := p.SeedStep(); s == nil || s.Cmd != "s" || s.Service != "web" {
		t.Fatalf("seed %+v", s)
	}
	if p.Steps[4].Text != "l" || p.Status != PlanRecorded {
		t.Fatalf("note/status %+v", p)
	}
	if FromRecipe(Recipe{SeedOnce: "none"}).SeedStep() != nil {
		t.Fatal("none must yield no data step")
	}
	e := FromRecipe(Recipe{})
	if len(e.Steps) != 2 || e.Steps[0].Kind != KindPrepare || len(e.Steps[0].Args) != 0 || e.Steps[1].Kind != KindComposeUp {
		t.Fatalf("empty: %+v", e.Steps)
	}
	if FromRecipe(Recipe{SeedOnce: "s", SeedPending: true}).Status != PlanProposed {
		t.Fatal("pending must be proposed")
	}
}

func TestForModeAndSupports(t *testing.T) {
	p := Plan{Steps: []Step{
		{ID: "up", Kind: KindComposeUp, Modes: []string{ModeContainer}},
		{ID: "up-deps", Kind: KindComposeUp, Modes: []string{ModeHybrid}, Exclude: []string{"web"}},
		{ID: "app", Kind: KindHost, Modes: []string{ModeHybrid}, Cmd: "bun run dev"},
	}}
	run, skipped := p.ForMode(ModeContainer)
	if len(run) != 1 || len(skipped) != 2 {
		t.Fatalf("run %d skipped %d", len(run), len(skipped))
	}
	if !p.SupportsMode(ModeHybrid) {
		t.Fatal("hybrid should be supported")
	}
	q := Plan{Steps: p.Steps[:2]}
	if q.SupportsMode(ModeHybrid) || !q.SupportsMode(ModeContainer) {
		t.Fatal("no host step: container only")
	}
}

func TestUpsertRemove(t *testing.T) {
	p := Plan{Steps: []Step{{ID: "a", Kind: KindNote}, {ID: "c", Kind: KindNote}}}
	p.Upsert(Step{ID: "b", Kind: KindNote}, "c")
	p.Upsert(Step{ID: "a", Kind: KindNote, Text: "x"}, "")
	p.Upsert(Step{ID: "d", Kind: KindNote}, "")
	var ids []string
	for _, s := range p.Steps {
		ids = append(ids, s.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c", "d"}) || p.Steps[0].Text != "x" {
		t.Fatalf("ids %v", ids)
	}
	if !p.Remove("b") || p.Remove("b") || len(p.Steps) != 3 {
		t.Fatal("remove")
	}
}

func TestValidatePlanNoSeedWithDataStep(t *testing.T) {
	p := Plan{NoSeed: true, Steps: []Step{{ID: "seed", Kind: KindExec, Cmd: "s", Once: OnceData}}}
	if err := ValidatePlan(p); err == nil || !strings.Contains(err.Error(), "no_seed") {
		t.Fatalf("err %v", err)
	}
	if p := FromRecipe(Recipe{SeedOnce: "none"}); !p.NoSeed || p.SeedStep() != nil {
		t.Fatalf("none: %+v", p)
	}
	if p := FromRecipe(Recipe{}); p.NoSeed {
		t.Fatal("empty seed is undecided, not never")
	}
}

func TestValidatePlanPort(t *testing.T) {
	ok := Plan{Steps: []Step{{ID: "a", Kind: KindHost, Cmd: "x", Port: 3000}}}
	if err := ValidatePlan(ok); err != nil {
		t.Fatal(err)
	}
	for _, st := range []Step{
		{ID: "a", Kind: KindHost, Cmd: "x", Port: 70000},
		{ID: "a", Kind: KindHost, Cmd: "x", Port: -1},
		{ID: "a", Kind: KindNote, Port: 80},
	} {
		if ValidatePlan(Plan{Steps: []Step{st}}) == nil {
			t.Errorf("expected refusal for %+v", st)
		}
	}
}

func TestValidatePlanRefusesControlCharacters(t *testing.T) {
	for _, st := range []Step{
		{ID: "a\nb", Kind: KindNote},
		{ID: "a", Kind: KindHost, Cmd: "x", Name: "n\x1b[2J"},
		{ID: "a", Kind: KindHost, Cmd: "x", Dir: "d\n"},
		{ID: "a", Kind: KindPrepare, Args: []string{"--x\ny"}},
		{ID: "a", Kind: KindHost, Cmd: "x", PortEnv: []string{"P\nQ"}},
	} {
		if ValidatePlan(Plan{Steps: []Step{st}}) == nil {
			t.Errorf("expected refusal for %+v", st)
		}
	}
}

// Fields the trust hash does not cover keep loading: an existing multi-line
// migrate/seed command or a login line with a tab must not break the file.
func TestLoadKeepsMultilineUntrustedFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "knowledge.json")
	os.WriteFile(p, []byte(`{"schema":1,"status":"verified","recipe":{"migrate":"a\nb","seed_once":"seed\n  more","login":["user\tadmin"]}}`), 0o644)
	k, err := Load(p)
	if err != nil || len(k.Plan.Steps) < 3 {
		t.Fatalf("%v %+v", err, k)
	}
	for _, st := range []Step{
		{ID: "a", Kind: KindExec, Cmd: "x\ny", Service: "s\t"},
		{ID: "a", Kind: KindNote, Text: "t\nu"},
		{ID: "a", Kind: KindComposeUp, Exclude: []string{"e\n"}},
	} {
		if err := ValidatePlan(Plan{Steps: []Step{st}}); err != nil {
			t.Errorf("%+v: %v", st, err)
		}
	}
}

func TestLoadFirstReportsBrokenFileNotMissing(t *testing.T) {
	good, bad, none := t.TempDir(), t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(bad, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(bad, ".stackfork", "knowledge.json"), []byte("{nope"), 0o644)
	if k, err := LoadFirst(none, none); k != nil || err != nil {
		t.Fatalf("missing is not an error: %v %v", k, err)
	}
	if _, err := LoadFirst(bad, good); err == nil || !strings.Contains(err.Error(), "knowledge.json") {
		t.Fatalf("broken file must be reported: %v", err)
	}
}
