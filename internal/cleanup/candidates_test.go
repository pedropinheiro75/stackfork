package cleanup

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func branches(cs []Candidate) string {
	var b []string
	for _, c := range cs {
		b = append(b, c.Branch)
	}
	sort.Strings(b)
	return strings.Join(b, ",")
}

func TestCandidates(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "feature-login", 1)

	// never pushed: no upstream
	nu := filepath.Join(f.Main, ".worktrees", "scratch")
	run(t, f.Main, "worktree", "add", "-q", "-b", "scratch", nu, "main")

	// locked
	lk := f.branch(t, "locked-one", 1)
	run(t, f.Main, "worktree", "lock", lk)

	// detached
	dt := filepath.Join(f.Main, ".worktrees", "detached")
	run(t, f.Main, "worktree", "add", "-q", "--detach", dt, "main")

	got, err := Candidates(f.Main)
	if err != nil {
		t.Fatal(err)
	}
	if branches(got) != "feature-login" {
		t.Fatalf("got %q", branches(got))
	}
	if got[0].Upstream != "origin/feature-login" || got[0].Head == "" {
		t.Fatalf("%+v", got[0])
	}
}

func TestCandidatesExcludesDefaultBranchAndMainCheckoutBranch(t *testing.T) {
	f := newFixture(t)
	// the main checkout moves to trunk, so main can be a linked worktree
	run(t, f.Main, "checkout", "-q", "-b", "trunk")
	run(t, f.Main, "push", "-q", "-u", "origin", "trunk")
	onDefault := filepath.Join(f.Main, ".worktrees", "main")
	run(t, f.Main, "worktree", "add", "-q", onDefault, "main")
	// the same branch checked out twice (git needs -f)
	twice := filepath.Join(f.Main, ".worktrees", "trunk-copy")
	run(t, f.Main, "worktree", "add", "-q", "-f", twice, "trunk")
	f.branch(t, "feature-login", 1)

	got, err := Candidates(f.Main)
	if err != nil {
		t.Fatal(err)
	}
	if branches(got) != "feature-login" {
		t.Fatalf("got %q", branches(got))
	}
}

func TestCandidatesMainCheckoutIsNeverOne(t *testing.T) {
	f := newFixture(t)
	run(t, f.Main, "checkout", "-q", "-b", "feature-main")
	run(t, f.Main, "push", "-q", "-u", "origin", "feature-main")
	got, _ := Candidates(f.Main)
	if len(got) != 0 {
		t.Fatalf("got %q", branches(got))
	}
}

func TestCandidatesSkipsAMissingFolder(t *testing.T) {
	f := newFixture(t)
	wt := f.branch(t, "gone-dir", 1)
	rmAll(t, wt)
	got, _ := Candidates(f.Main)
	if len(got) != 0 {
		t.Fatalf("got %q", branches(got))
	}
}
