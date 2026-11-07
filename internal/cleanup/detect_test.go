package cleanup

import (
	"strings"
	"testing"
	"time"
)

// settle pushes main, deletes the branch on origin when asked, and fetches
// with prune, as the loop would.
func (f fixture) settle(t *testing.T, branch string, deleteRemote bool) {
	t.Helper()
	run(t, f.Main, "push", "-q", "origin", "main")
	if deleteRemote {
		run(t, f.Main, "push", "-q", "origin", "--delete", branch)
	}
	if err := Fetch(f.Main, "origin", 20*time.Second); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) detect(t *testing.T, branch string) Result {
	t.Helper()
	cs, err := Candidates(f.Main)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Detect(f.Main, cs) {
		if r.Branch == branch {
			return r
		}
	}
	t.Fatalf("%s is not a candidate", branch)
	return Result{}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name         string
		deleteRemote bool
		merge        func(t *testing.T, f fixture)
		state, via   string
	}{
		{"merge commit", true, func(t *testing.T, f fixture) {
			run(t, f.Main, "commit", "-q", "--allow-empty", "-m", "unrelated")
			run(t, f.Main, "merge", "-q", "--no-ff", "-m", "merge feature", "feature-login")
		}, Merged, ViaAncestry},
		{"fast-forward", true, func(t *testing.T, f fixture) {
			run(t, f.Main, "merge", "-q", "--ff-only", "feature-login")
		}, Merged, ViaAncestry},
		{"rebase merge", true, func(t *testing.T, f fixture) {
			commit(t, f.Main, "other.txt", "o\n", "unrelated")
			run(t, f.Main, "cherry-pick", "main..feature-login")
		}, Merged, ViaCherry},
		{"squash merge", true, func(t *testing.T, f fixture) {
			commit(t, f.Main, "other.txt", "o\n", "unrelated")
			run(t, f.Main, "merge", "-q", "--squash", "feature-login")
			run(t, f.Main, "commit", "-q", "-m", "feature-login (#42)")
		}, Merged, ViaPatchID},
		{"deleted without merge", true, func(t *testing.T, f fixture) {
			commit(t, f.Main, "other.txt", "o\n", "unrelated")
		}, Closed, ""},
		{"merged but the remote branch is kept", false, func(t *testing.T, f fixture) {
			run(t, f.Main, "merge", "-q", "--ff-only", "feature-login")
		}, Open, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.branch(t, "feature-login", 2)
			c.merge(t, f)
			f.settle(t, "feature-login", c.deleteRemote)
			if r := f.detect(t, "feature-login"); r.State != c.state || r.Via != c.via {
				t.Fatalf("got %+v, want %s via %q", r, c.state, c.via)
			}
		})
	}
}

func TestDetectSquashBeyondTheCommitCapIsNotMerged(t *testing.T) {
	defer func(n int) { maxSquashCommits = n }(maxSquashCommits)
	maxSquashCommits = 5
	f := newFixture(t)
	f.branch(t, "feature-login", 2)
	run(t, f.Main, "merge", "-q", "--squash", "feature-login")
	run(t, f.Main, "commit", "-q", "-m", "squash")
	// A later edit of the branch's file keeps the merge-tree proof from
	// answering, then more commits than the window land after the squash.
	commit(t, f.Main, "feature-login-a.txt", "edited later\n", "later edit")
	for i := 0; i < maxSquashCommits; i++ {
		run(t, f.Main, "commit", "-q", "--allow-empty", "-m", "later")
	}
	f.settle(t, "feature-login", true)
	if r := f.detect(t, "feature-login"); r.State != Closed {
		t.Fatalf("got %+v", r)
	}
}

// git cherry ignores merge commits, so a merge that carries content (here
// evil.txt) must not pass for a rebase merge.
func TestDetectContentCarryingMergeCommitIsNotMergedViaCherry(t *testing.T) {
	f := newFixture(t)
	wt := f.branch(t, "feature-login", 1)
	commit(t, f.Main, "other.txt", "o\n", "unrelated")
	run(t, f.Main, "push", "-q", "origin", "main")
	run(t, wt, "fetch", "-q", "origin")
	run(t, wt, "merge", "-q", "--no-ff", "--no-commit", "origin/main")
	put(t, wt, "evil.txt", "not reviewed\n")
	run(t, wt, "add", "evil.txt")
	run(t, wt, "commit", "-q", "-m", "merge main")
	run(t, f.Main, "cherry-pick", run(t, wt, "rev-parse", "feature-login^1"))
	f.settle(t, "feature-login", true)
	if r := f.detect(t, "feature-login"); r.State != Closed {
		t.Fatalf("got %+v", r)
	}
}

func TestDetectWritesNoObjects(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "feature-login", 2)
	commit(t, f.Main, "other.txt", "o\n", "unrelated")
	run(t, f.Main, "merge", "-q", "--squash", "feature-login")
	run(t, f.Main, "commit", "-q", "-m", "squash")
	f.settle(t, "feature-login", true)
	before := run(t, f.Main, "count-objects", "-v")
	if r := f.detect(t, "feature-login"); r.State != Merged {
		t.Fatalf("got %+v", r)
	}
	if after := run(t, f.Main, "count-objects", "-v"); after != before {
		t.Fatalf("objects changed:\n%s\n--- after\n%s", before, after)
	}
}

func TestRemoteIsTheDefaultBranchsRemote(t *testing.T) {
	f := newFixture(t)
	if got := Remote(f.Main); got != "origin" {
		t.Fatal(got)
	}
	run(t, f.Main, "remote", "rename", "origin", "upstream")
	if got := Remote(f.Main); !strings.HasPrefix(got, "upstream") {
		t.Fatalf("got %q", got)
	}
}
