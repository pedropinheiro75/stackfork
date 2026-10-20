package live

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func setup(t *testing.T) (root, inst string, p *Poller) {
	root, inst = tmp(t), tmp(t)
	write(t, root, "bun.lock", "v1")
	write(t, root, "Dockerfile", "FROM scratch")
	p = NewPoller(root, []Entry{entry(root, "bun.lock", CatBuild), entry(root, "Dockerfile", CatBuild)}, nil)
	p.Poll()
	return
}

func TestBaselineWrittenOnFirstSightOnly(t *testing.T) {
	root, inst, p := setup(t)
	b, created, err := FirstSight(inst, "feature-login", "3f9a2c1", p)
	if err != nil || !created || b.Branch != "feature-login" || b.Upstream != "3f9a2c1" || len(b.Digests) != 2 {
		t.Fatalf("first sight: %+v %v %v", b, created, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(inst, BaselineFile)); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
	write(t, root, "bun.lock", "v2-changed")
	p.Poll()
	b2, created, err := FirstSight(inst, "other", "zzz", p)
	if err != nil || created || b2.Branch != "feature-login" || b2.Digests["bun.lock"] != b.Digests["bun.lock"] {
		t.Fatalf("second sight rewrote the baseline: %+v %v %v", b2, created, err)
	}
}

func TestBaselineMovesOnlyAfterSucceededLiveRunForCoveredPaths(t *testing.T) {
	root, inst, p := setup(t)
	b, _, _ := FirstSight(inst, "feature-login", "", p)
	p.SetBaseline(b.Digests)
	old := b.Digests["bun.lock"]
	write(t, root, "bun.lock", "v2-changed")
	write(t, root, "Dockerfile", "FROM alpine")
	ch := p.Poll()
	if len(ch) != 2 {
		t.Fatal(ch)
	}
	stored := func() *Baseline { x, _ := LoadBaseline(inst); return x }

	for _, c := range []struct{ trigger, outcome string }{
		{"live", "failed"}, {"live", "interrupted"}, {"cli", "succeeded"}, {"dashboard", "succeeded"},
	} {
		if err := b.RecordRun(inst, c.trigger, c.outcome, ch, "newcommit"); err != nil {
			t.Fatal(err)
		}
		if got := stored(); got.Digests["bun.lock"] != old || got.Upstream != "" {
			t.Fatalf("%s/%s moved the baseline", c.trigger, c.outcome)
		}
	}

	var covered []Change // the run covered only bun.lock
	for _, c := range ch {
		if c.Path == "bun.lock" {
			covered = append(covered, c)
		}
	}
	if err := b.RecordRun(inst, "live", "succeeded", covered, "newcommit"); err != nil {
		t.Fatal(err)
	}
	got := stored()
	if got.Digests["bun.lock"] == old || got.Digests["bun.lock"] != covered[0].Digest {
		t.Fatal("covered path did not move")
	}
	if got.Digests["Dockerfile"] != b.Digests["Dockerfile"] || got.Upstream != "newcommit" {
		t.Fatalf("uncovered path moved or upstream missing: %+v", got)
	}
	p.SetBaseline(got.Digests)
	if rest := p.Poll(); len(rest) != 1 || rest[0].Path != "Dockerfile" {
		t.Fatalf("the uncovered change must stay pending: %v", rest)
	}
}

func TestBaselineRemovedPathLeavesTheBaseline(t *testing.T) {
	root, inst, p := setup(t)
	b, _, _ := FirstSight(inst, "b", "", p)
	p.SetBaseline(b.Digests)
	os.Remove(filepath.Join(root, "Dockerfile"))
	ch := p.Poll()
	if err := b.RecordRun(inst, "live", "succeeded", ch, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadBaseline(inst); got.Digests["Dockerfile"] != "" {
		t.Fatal("removed path kept")
	}
}
