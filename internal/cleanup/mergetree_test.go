package cleanup

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// stack makes feature-invoices (2 commits) and feature-invoices-ui cut from it
// (2 more), both pushed.
func (f fixture) stack(t *testing.T) {
	t.Helper()
	f.branch(t, "feature-invoices", 2)
	b := filepath.Join(f.Main, ".worktrees", "feature-invoices-ui")
	run(t, f.Main, "worktree", "add", "-q", "-b", "feature-invoices-ui", b, "feature-invoices")
	commit(t, b, "ui-a.txt", "x\n", "ui a")
	commit(t, b, "ui-b.txt", "x\n", "ui b")
	run(t, b, "push", "-q", "-u", "origin", "feature-invoices-ui")
}

func squash(t *testing.T, f fixture, branch string) {
	t.Helper()
	run(t, f.Main, "merge", "-q", "--squash", branch)
	run(t, f.Main, "commit", "-q", "-m", branch+" (#1)")
}

func objectFiles(t *testing.T, f fixture) string {
	t.Helper()
	var names []string
	filepath.WalkDir(filepath.Join(f.Main, ".git", "objects"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	return strings.Join(names, "\n")
}

// Both pull requests of a stack are squash-merged: the second one has no
// commit in main with its patch id, but merging it adds nothing.
func TestDetectStackedSquashMergesProveTheSecondByMergeTree(t *testing.T) {
	f := newFixture(t)
	f.stack(t)
	squash(t, f, "feature-invoices")
	squash(t, f, "feature-invoices-ui")
	f.settle(t, "feature-invoices", true)
	f.settle(t, "feature-invoices-ui", true)
	if r := f.detect(t, "feature-invoices"); r.State != Merged || r.Via != ViaPatchID {
		t.Fatalf("first of the stack: %+v", r)
	}
	before := objectFiles(t, f)
	r := f.detect(t, "feature-invoices-ui")
	if r.State != Merged || r.Via != ViaMergeTree {
		t.Fatalf("second of the stack: %+v", r)
	}
	if after := objectFiles(t, f); after != before {
		t.Fatalf("detection wrote objects:\n%s\n--- after\n%s", before, after)
	}
}

func TestDetectBranchWithWorkNotInTargetStaysClosed(t *testing.T) {
	f := newFixture(t)
	f.stack(t)
	squash(t, f, "feature-invoices") // only the first half of the stack lands
	f.settle(t, "feature-invoices-ui", true)
	if r := f.detect(t, "feature-invoices-ui"); r.State != Closed {
		t.Fatalf("got %+v", r)
	}
}

// A squash merge that was reverted on the target. The patch-id proof still
// answers merged (the squash commit is in main's history and nothing looks at
// the revert): that is today's behavior and this change keeps it. The new
// proof must not say merged, because merging the branch brings the work back.
func TestDetectSquashThenRevertIsNotProvenByMergeTree(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "feature-invoices", 2)
	squash(t, f, "feature-invoices")
	run(t, f.Main, "revert", "--no-edit", "HEAD")
	f.settle(t, "feature-invoices", true)
	if mergeAddsNothing(f.Main, "refs/heads/feature-invoices", "origin/main") {
		t.Fatal("the merge-tree proof accepted a reverted squash merge")
	}
	if r := f.detect(t, "feature-invoices"); r.State != Merged || r.Via != ViaPatchID {
		t.Fatalf("patch-id answer changed: %+v", r)
	}
}

func TestDetectConflictWithTargetStaysClosed(t *testing.T) {
	f := newFixture(t)
	wt := f.branch(t, "feature-invoices", 0)
	commit(t, wt, "app.txt", "branch\n", "edit app")
	run(t, wt, "push", "-q", "origin", "feature-invoices")
	commit(t, f.Main, "app.txt", "target\n", "edit app differently")
	f.settle(t, "feature-invoices", true)
	if mergeAddsNothing(f.Main, "refs/heads/feature-invoices", "origin/main") {
		t.Fatal("a conflicting merge counted as adding nothing")
	}
	if r := f.detect(t, "feature-invoices"); r.State != Closed {
		t.Fatalf("got %+v", r)
	}
}

// A custom merge driver can resolve any conflict as "ours", which would make
// the merged tree equal the target's: the proof steps aside.
func TestDetectMergeDriverInConfigDisablesMergeTreeProof(t *testing.T) {
	f := newFixture(t)
	commit(t, f.Main, ".gitattributes", "app.txt merge=keep\n", "attributes")
	run(t, f.Main, "config", "merge.keep.driver", "true")
	wt := f.branch(t, "feature-invoices", 0)
	commit(t, wt, "app.txt", "branch\n", "edit app")
	run(t, wt, "push", "-q", "origin", "feature-invoices")
	commit(t, f.Main, "app.txt", "target\n", "edit app differently")
	f.settle(t, "feature-invoices", true)
	if mergeAddsNothing(f.Main, "refs/heads/feature-invoices", "origin/main") {
		t.Fatal("the proof ran with a merge driver configured")
	}
	if r := f.detect(t, "feature-invoices"); r.State != Closed {
		t.Fatalf("got %+v", r)
	}
}

// A commit made after the pull request was merged is work that no proof
// covers: the branch is closed, and a cleanup of it is refused.
func TestDetectLocalCommitAfterTheMergeStaysNotEligible(t *testing.T) {
	f := newFixture(t)
	wt := f.branch(t, "feature-invoices", 2)
	commit(t, f.Main, "other.txt", "o\n", "unrelated")
	squash(t, f, "feature-invoices")
	f.settle(t, "feature-invoices", true)
	cands, _ := Candidates(f.Main)
	r := f.detect(t, "feature-invoices")
	if r.State != Merged {
		t.Fatalf("before the late commit: %+v", r)
	}
	commit(t, wt, "late.txt", "x\n", "late")
	if now := f.detect(t, "feature-invoices"); now.State != Closed {
		t.Fatalf("after the late commit: %+v", now)
	}
	if v := Evaluate(Check{Main: f.Main, Candidate: cands[0], Result: r}); v.Eligible {
		t.Fatalf("eligible: %+v", v)
	}
}

// The branch merged the target into itself, then was squash-merged with a
// reviewer's extra file, so no commit of main has the branch's patch id.
func TestDetectBranchWithMergeFromTargetInsideProvedByMergeTree(t *testing.T) {
	f := newFixture(t)
	wt := f.branch(t, "feature-invoices", 1)
	commit(t, f.Main, "other.txt", "o\n", "unrelated")
	run(t, f.Main, "push", "-q", "origin", "main")
	run(t, wt, "fetch", "-q", "origin")
	run(t, wt, "merge", "-q", "--no-ff", "-m", "merge main", "origin/main")
	run(t, wt, "push", "-q", "origin", "feature-invoices")
	run(t, f.Main, "merge", "-q", "--squash", "feature-invoices")
	put(t, f.Main, "review.txt", "added by the reviewer\n")
	run(t, f.Main, "add", "review.txt")
	run(t, f.Main, "commit", "-q", "-m", "feature-invoices (#2)")
	f.settle(t, "feature-invoices", true)
	if r := f.detect(t, "feature-invoices"); r.State != Merged || r.Via != ViaMergeTree {
		t.Fatalf("got %+v", r)
	}
}

// The forge says MERGED but git cannot prove it (here only half of a stack
// landed): the text names the branch and the default branch, never says
// "closed", and nothing is eligible. The forge is not a proof.
func TestEvaluateForgeMergedWithoutAGitProofSaysSo(t *testing.T) {
	f := newFixture(t)
	f.stack(t)
	squash(t, f, "feature-invoices")
	f.settle(t, "feature-invoices-ui", true)
	cs, _ := Candidates(f.Main)
	var c Candidate
	for _, x := range cs {
		if x.Branch == "feature-invoices-ui" {
			c = x
		}
	}
	r := f.detect(t, "feature-invoices-ui")
	if r.State != Closed {
		t.Fatalf("git proved it: %+v", r)
	}
	r.Forge = "MERGED"
	v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r})
	want := "PR merged, but feature-invoices-ui has work that is not in main; check it before you delete anything"
	if v.Eligible || v.Warning != want {
		t.Fatalf("got %+v, want warning %q", v, want)
	}
	r.Forge = "CLOSED"
	if v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r}); v.Warning != "PR closed without merge" {
		t.Fatalf("forge CLOSED: %+v", v)
	}
}
