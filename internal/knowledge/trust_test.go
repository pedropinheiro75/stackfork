package knowledge

import "testing"

func TestPlanHashCoversHostAndPrepareOnly(t *testing.T) {
	base := []Step{
		{ID: "prep", Kind: KindPrepare, Args: []string{"-f", "a.yaml"}},
		{ID: "up", Kind: KindComposeUp, Exclude: []string{"app"}},
		{ID: "mig", Kind: KindExec, Service: "app", Cmd: "migrate"},
		{ID: "app", Kind: KindHost, Cmd: "npm run dev", Modes: []string{"hybrid", "container"}},
	}
	h := PlanHash(base)
	mod := func(f func(s []Step)) string {
		c := append([]Step(nil), base...)
		f(c)
		return PlanHash(c)
	}
	if mod(func(s []Step) { s[2].Cmd = "other" }) != h {
		t.Error("exec cmd must not change the hash")
	}
	if mod(func(s []Step) { s[3].Cmd = "rm -rf /" }) == h {
		t.Error("host cmd must change the hash")
	}
	if mod(func(s []Step) { s[0].Args = []string{"-f", "b.yaml"} }) == h {
		t.Error("prepare arg must change the hash")
	}
	if mod(func(s []Step) { s[3].Modes = []string{"container", "hybrid"} }) != h {
		t.Error("mode order must not change the hash")
	}
}

func TestTrustFileRoundTrip(t *testing.T) {
	base := t.TempDir()
	p := TrustPath(base, "/some/repo")
	if _, _, ok := ReadTrust(p); ok {
		t.Fatal("no file yet")
	}
	steps := []Step{{ID: "app", Kind: KindHost, Cmd: "x"}}
	if err := WriteTrust(p, PlanHash(steps), steps); err != nil {
		t.Fatal(err)
	}
	h, date, ok := ReadTrust(p)
	if !ok || h != PlanHash(steps) || date == "" {
		t.Fatalf("got %q %q %v", h, date, ok)
	}
	if got := ReadTrustSteps(p); len(got) != 1 || got[0].Cmd != "x" {
		t.Fatalf("steps %+v", got)
	}
}

// A plan without on: host steps keeps the hash it had before add-host-exec,
// so existing trust files stay valid.
func TestPlanHashGoldenWithoutHostExec(t *testing.T) {
	steps := []Step{
		{ID: "prep", Kind: KindPrepare, Args: []string{"-f", "a.yaml"}},
		{ID: "up", Kind: KindComposeUp, Exclude: []string{"app"}},
		{ID: "mig", Kind: KindExec, Service: "app", Cmd: "migrate", Once: OnceSchema},
		{ID: "app", Kind: KindHost, Cmd: "npm run dev", Modes: []string{"hybrid", "container"}, Port: 3000},
	}
	if got := PlanHash(steps); got != "eb157c0aa288b508e15842f8df4bed5f51e7f4fc5e36ec0a92e79620009a4a35" {
		t.Fatalf("hash changed: %s", got)
	}
}

func TestPlanHashCoversHostExec(t *testing.T) {
	steps := []Step{{ID: "mig", Kind: KindExec, On: OnHost, Cmd: "bun run db:migrate", Once: OnceSchema}}
	if got := TrustSteps(steps); len(got) != 1 || got[0].On != OnHost {
		t.Fatalf("host exec step must be trusted: %+v", got)
	}
	for name, change := range map[string]func(*Step){
		"cmd":  func(s *Step) { s.Cmd = "other" },
		"dir":  func(s *Step) { s.Dir = "apps/api" },
		"once": func(s *Step) { s.Once = OnceData },
		"on":   func(s *Step) { s.On = "" },
	} {
		changed := []Step{steps[0], {ID: "host", Kind: KindHost, Name: "app", Cmd: "npm run dev"}}
		base := PlanHash(changed)
		change(&changed[0])
		if PlanHash(changed) == base {
			t.Errorf("host exec %s must change the hash", name)
		}
	}
	if got := (Plan{Steps: []Step{{ID: "c", Kind: KindExec, Cmd: "x"}, steps[0]}}).HostRunSteps(); len(got) != 1 || got[0].ID != "mig" {
		t.Fatalf("HostRunSteps %+v", got)
	}
}
