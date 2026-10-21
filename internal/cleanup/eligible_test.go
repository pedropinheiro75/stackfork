package cleanup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergedFx returns a fixture whose feature-login branch was squash-merged into
// origin/main and deleted there, and its detected candidate.
func mergedFx(t *testing.T) (fixture, Candidate, Result) {
	t.Helper()
	return mergedFxWith(t)
}

// mergedFxWith first commits the given files (path, body pairs) to main.
func mergedFxWith(t *testing.T, files ...string) (fixture, Candidate, Result) {
	t.Helper()
	f := newFixture(t)
	for i := 0; i+1 < len(files); i += 2 {
		commit(t, f.Main, files[i], files[i+1], "add "+files[i])
	}
	if len(files) > 0 {
		run(t, f.Main, "push", "-q", "origin", "main")
	}
	f.branch(t, "feature-login", 2)
	commit(t, f.Main, "other.txt", "o\n", "unrelated")
	run(t, f.Main, "merge", "-q", "--squash", "feature-login")
	run(t, f.Main, "commit", "-q", "-m", "feature-login (#42)")
	f.settle(t, "feature-login", true)
	cs, err := Candidates(f.Main)
	if err != nil || len(cs) != 1 {
		t.Fatalf("candidates: %+v %v", cs, err)
	}
	r := f.detect(t, "feature-login")
	if r.State != Merged {
		t.Fatalf("not merged: %+v", r)
	}
	return f, cs[0], r
}

func TestEvaluateEligibility(t *testing.T) {
	cases := []struct {
		name    string
		auto    bool
		adapter []string
		prep    func(t *testing.T, f fixture, c Candidate)
		locked  string
		forge   string
		want    string // warning; "" means eligible
	}{
		{name: "clean"},
		{name: "tracked file edited", prep: func(t *testing.T, f fixture, c Candidate) { put(t, c.Path, "app.txt", "changed\n") },
			want: "PR merged, but this worktree has uncommitted changes"},
		{name: "untracked file", prep: func(t *testing.T, f fixture, c Candidate) { put(t, c.Path, "notes/todo.txt", "x") },
			want: "PR merged, but this worktree has uncommitted changes"},
		{name: "staged file", prep: func(t *testing.T, f fixture, c Candidate) {
			put(t, c.Path, "new.txt", "x")
			run(t, c.Path, "add", "new.txt")
		}, want: "PR merged, but this worktree has uncommitted changes"},
		{name: "adapter copy equal to main", adapter: []string{".agent-dev"}, prep: func(t *testing.T, f fixture, c Candidate) {
			put(t, f.Main, ".agent-dev/app.compose.yaml", "services: {}\n")
			put(t, c.Path, ".agent-dev/app.compose.yaml", "services: {}\n")
		}},
		{name: "adapter copy edited", adapter: []string{".agent-dev"}, prep: func(t *testing.T, f fixture, c Candidate) {
			put(t, f.Main, ".agent-dev/app.compose.yaml", "services: {}\n")
			put(t, c.Path, ".agent-dev/app.compose.yaml", "services: {web: {}}\n")
		}, want: "PR merged, but this worktree has uncommitted changes"},
		{name: "unlisted file equal to main", prep: func(t *testing.T, f fixture, c Candidate) {
			put(t, f.Main, "scratch.txt", "x")
			put(t, c.Path, "scratch.txt", "x")
		}, want: "PR merged, but this worktree has uncommitted changes"},
		{name: "commit after the merge", prep: func(t *testing.T, f fixture, c Candidate) {
			commit(t, c.Path, "late.txt", "x\n", "late")
		}, want: "PR merged, but this branch has commits that are not in main"},
		{name: "branch switched", prep: func(t *testing.T, f fixture, c Candidate) { run(t, c.Path, "switch", "-q", "-c", "other") },
			want: "this worktree no longer has feature-login checked out"},
		{name: "run lock held", locked: "a run holds the instance's run lock", want: "PR merged, but a run holds the instance's run lock"},
		{name: "forge says closed", forge: "CLOSED", want: "PR closed without merge"},
		{name: "ignored file in suggest", prep: func(t *testing.T, f fixture, c Candidate) { ignore(t, c.Path, ".env.local") }},
		{name: "ignored file blocks auto", auto: true, prep: func(t *testing.T, f fixture, c Candidate) { ignore(t, c.Path, ".env.local") },
			want: "PR merged, but this worktree has ignored files (.env.local)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c, r := mergedFx(t)
			if tc.prep != nil {
				tc.prep(t, f, c)
			}
			r.Forge = tc.forge
			v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r, Auto: tc.auto, Adapter: tc.adapter,
				Locked: func() string { return tc.locked }})
			if v.Warning != tc.want || v.Eligible != (tc.want == "") {
				t.Fatalf("eligible=%v warning=%q, want %q (dirty %v ignored %v)", v.Eligible, v.Warning, tc.want, v.Dirty, v.Ignored)
			}
		})
	}
}

// ignore writes a gitignored file, committing the .gitignore line first.
func ignore(t *testing.T, wt, rel string) {
	t.Helper()
	top := strings.Split(rel, "/")[0]
	exclude := run(t, wt, "rev-parse", "--git-path", "info/exclude")
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(wt, exclude)
	}
	os.MkdirAll(filepath.Dir(exclude), 0o755)
	fh, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fh.WriteString("/" + top + "\n")
	fh.Close()
	put(t, wt, rel, "x")
}

func TestEvaluateCacheFoldersAndListedIgnoredFiles(t *testing.T) {
	f, c, r := mergedFx(t)
	ignore(t, c.Path, "node_modules/x/index.js")
	ignore(t, c.Path, ".env.local")
	v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r, Auto: true})
	if v.Eligible || !strings.Contains(v.Warning, ".env.local") || strings.Contains(v.Warning, "node_modules") {
		t.Fatalf("auto with .env.local: %+v", v)
	}
	v = Evaluate(Check{Main: f.Main, Candidate: c, Result: r})
	if !v.Eligible || len(v.Ignored) != 1 || v.Ignored[0] != ".env.local" {
		t.Fatalf("suggest must list only .env.local (node_modules is a cache folder): %+v", v)
	}
}

func TestEvaluateSeesAnEditMadeAfterTheFirstCheck(t *testing.T) {
	f, c, r := mergedFx(t)
	chk := Check{Main: f.Main, Candidate: c, Result: r}
	if v := Evaluate(chk); !v.Eligible {
		t.Fatalf("first check: %+v", v)
	}
	put(t, c.Path, "app.txt", "edited between the check and the run\n")
	if v := Evaluate(chk); v.Eligible || !strings.Contains(v.Warning, "uncommitted") {
		t.Fatalf("re-check must fail: %+v", v)
	}
}

func TestRemoveWorktreeRefusesChangesAndDeleteBranchProvesTheMerge(t *testing.T) {
	f, c, _ := mergedFx(t)
	put(t, c.Path, "app.txt", "edited\n")
	if _, err := RemoveWorktree(f.Main, c.Path); err == nil {
		t.Fatal("git must refuse a worktree with changes (no --force)")
	}
	if _, err := os.Stat(c.Path); err != nil {
		t.Fatal("the refused removal deleted the worktree")
	}
	run(t, c.Path, "checkout", "--", "app.txt")
	if out, err := RemoveWorktree(f.Main, c.Path); err != nil {
		t.Fatalf("clean removal: %v %s", err, out)
	}
	// squash merge: `git branch -d` refuses, the patch proof allows -D
	if out, err := DeleteBranch(f.Main, "feature-login", Target(f.Main)); err != nil {
		t.Fatalf("delete: %v %s", err, out)
	}
	if run(t, f.Main, "branch", "--list", "feature-login") != "" {
		t.Fatal("branch still exists")
	}
}

func TestDeleteBranchWithoutProofKeepsIt(t *testing.T) {
	f := newFixture(t)
	f.branch(t, "spike", 1)
	run(t, f.Main, "push", "-q", "origin", "--delete", "spike")
	Fetch(f.Main, "origin", 20e9)
	run(t, f.Main, "worktree", "remove", filepath.Join(f.Main, ".worktrees", "spike"))
	if _, err := DeleteBranch(f.Main, "spike", Target(f.Main)); err == nil {
		t.Fatal("an unmerged branch must not be force-deleted")
	}
	if run(t, f.Main, "branch", "--list", "spike") == "" {
		t.Fatal("the unmerged branch is gone")
	}
}

func excludeLine(t *testing.T, wt, line string) {
	t.Helper()
	exclude := run(t, wt, "rev-parse", "--git-path", "info/exclude")
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(wt, exclude)
	}
	os.MkdirAll(filepath.Dir(exclude), 0o755)
	fh, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fh.WriteString(line + "\n")
	fh.Close()
}

// An ignored cache-named folder that holds a git repository is somebody's
// work: it is listed and blocks `auto`.
func TestEvaluateNestedRepoInAnIgnoredCacheFolderIsListedAndBlocksAuto(t *testing.T) {
	f, c, r := mergedFx(t)
	ignore(t, c.Path, "vendor/lib/x.go")
	run(t, filepath.Join(c.Path, "vendor", "lib"), "init", "-q")
	v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r, Auto: true})
	if v.Eligible || !strings.Contains(v.Warning, "vendor/") {
		t.Fatalf("auto: %+v", v)
	}
	v = Evaluate(Check{Main: f.Main, Candidate: c, Result: r})
	if !v.Eligible || len(v.Ignored) != 1 || !strings.Contains(v.Ignored[0], "vendor/") || !strings.Contains(v.Ignored[0], "git repository") {
		t.Fatalf("suggest must list the nested repository: %+v", v)
	}
}

// A cache name is a cache folder only at the repository root or next to a
// package manifest; src/vendor is a source folder.
func TestEvaluateCacheFolderNamesCountOnlyAtKnownRoots(t *testing.T) {
	f, c, r := mergedFxWith(t, "src/main.go", "package main\n")
	excludeLine(t, c.Path, "/src/vendor")
	put(t, c.Path, "src/vendor/x.go", "package x\n")
	v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r, Auto: true})
	if v.Eligible || !strings.Contains(v.Warning, "src/vendor/") {
		t.Fatalf("src/vendor must not be a cache folder: %+v", v)
	}
}

func TestEvaluateCacheFolderBesideAManifestStaysACache(t *testing.T) {
	f, c, r := mergedFxWith(t, "web/package.json", "{}")
	excludeLine(t, c.Path, "/web/node_modules")
	put(t, c.Path, "web/node_modules/x/index.js", "x")
	if v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r, Auto: true}); !v.Eligible {
		t.Fatalf("web/node_modules is a dependency folder: %+v", v)
	}
	put(t, c.Path, "web/node_modules/dep/.git", "gitdir: elsewhere\n")
	if v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r, Auto: true}); v.Eligible {
		t.Fatalf("a git repository in a dependency folder blocks auto: %+v", v)
	}
}

// Git refuses to remove a worktree with an initialized submodule, so the
// cleanup would fail at the first step and pause live mode: not eligible.
func TestEvaluateInitializedSubmoduleIsNotEligible(t *testing.T) {
	f := newFixture(t)
	sub := filepath.Join(tmp(t), "sub")
	os.MkdirAll(sub, 0o755)
	run(t, sub, "init", "-q", "-b", "main")
	commit(t, sub, "s.txt", "s\n", "sub")
	run(t, f.Main, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "libs/sub")
	run(t, f.Main, "commit", "-q", "-m", "add submodule")
	run(t, f.Main, "push", "-q", "origin", "main")
	f.branch(t, "feature-login", 1)
	commit(t, f.Main, "other.txt", "o\n", "unrelated")
	run(t, f.Main, "merge", "-q", "--squash", "feature-login")
	run(t, f.Main, "commit", "-q", "-m", "feature-login (#42)")
	f.settle(t, "feature-login", true)
	cs, _ := Candidates(f.Main)
	c, r := cs[0], f.detect(t, "feature-login")
	if v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r}); !v.Eligible {
		t.Fatalf("an uninitialized submodule does not block: %+v", v)
	}
	run(t, c.Path, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
	v := Evaluate(Check{Main: f.Main, Candidate: c, Result: r})
	if v.Eligible || !strings.Contains(v.Warning, "submodule") {
		t.Fatalf("initialized submodule: %+v", v)
	}
}
