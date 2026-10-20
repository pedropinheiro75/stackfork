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
