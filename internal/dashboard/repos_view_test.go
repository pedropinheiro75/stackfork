package dashboard

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

func repoWith(readiness string, states ...string) repos.Repo {
	r := repos.Repo{Name: "x", Readiness: readiness}
	for _, s := range states {
		r.Worktrees = append(r.Worktrees, repos.Worktree{Instance: &inventory.InstanceRef{Project: "sf-x", State: s}})
		if s == inventory.StateRunning || s == inventory.StatePartial {
			r.Running++
		}
	}
	return r
}

func TestRepoStatusVocabulary(t *testing.T) {
	for _, c := range []struct {
		name         string
		repo         repos.Repo
		state, label string
	}{
		{"running", repoWith(repos.Ready, inventory.StateRunning), "ok", "1 running"},
		{"two running", repoWith(repos.Ready, inventory.StateRunning, inventory.StateRunning), "ok", "2 running"},
		{"partial", repoWith(repos.Ready, inventory.StatePartial), "attention", "1 partial"},
		{"stopped", repoWith(repos.Ready, inventory.StateStopped), "off", "Stopped"},
		{"needs-learn", repoWith(repos.NeedsLearn), "off", "Not set up"},
		{"unknown", repoWith(repos.Unknown), "off", "Not set up"},
		{"draft", repoWith(repos.Draft), "attention", "Setup unverified"},
		{"ready, no instance", repoWith(repos.Ready), "ok", "Ready"},
	} {
		if s, l := repoStatus(c.repo); s != c.state || l != c.label {
			t.Errorf("%s: got (%s, %s), want (%s, %s)", c.name, s, l, c.state, c.label)
		}
	}
}

func TestSortReposRunningThenRecent(t *testing.T) {
	now := time.Now()
	idleNew := repos.Repo{Name: "idle-new", LastCommit: now}
	runningOld := repos.Repo{Name: "running-old", Running: 1, LastCommit: now.Add(-48 * time.Hour)}
	b := repos.Repo{Name: "b", LastCommit: now.Add(-time.Hour)}
	a := repos.Repo{Name: "a", LastCommit: now.Add(-time.Hour)}
	in := []repos.Repo{idleNew, b, runningOld, a}
	got := sortRepos(in)
	want := []string{"running-old", "idle-new", "a", "b"}
	for i, n := range want {
		if got[i].Name != n {
			t.Fatalf("order %v, want %v", names(got), want)
		}
	}
	if in[0].Name != "idle-new" {
		t.Error("sortRepos must not reorder its input (the JSON order is the CLI's)")
	}
}

func names(rs []repos.Repo) (n []string) {
	for _, r := range rs {
		n = append(n, r.Name)
	}
	return
}

func TestAgo(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, "—"},
		{now.Add(-90 * time.Second), "1 min ago"},
		{now.Add(-2*time.Hour - time.Minute), "2 h ago"},
		{now.Add(-3*24*time.Hour - time.Minute), "3 d ago"},
		{now.Add(-21*24*time.Hour - time.Minute), "3 w ago"},
	} {
		if got := ago(c.t); got != c.want {
			t.Errorf("ago(%v) = %q, want %q", now.Sub(c.t), got, c.want)
		}
	}
}

func TestRelPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	repo := filepath.Join(home, "code", "acme")
	for _, c := range []struct{ path, want string }{
		{repo, "main checkout"},
		{filepath.Join(repo, ".worktrees", "x"), ".worktrees/x"},
		{filepath.Join(home, "other", "wt"), "~/other/wt"},
		{"/srv/elsewhere", "/srv/elsewhere"},
	} {
		if got := relPath(repo, c.path); got != c.want {
			t.Errorf("relPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestAheadBehindText(t *testing.T) {
	for _, c := range []struct {
		a, b     int
		upstream string
		want     string
	}{
		{3, 12, "origin/x", "3 ahead, 12 behind upstream"},
		{0, 0, "origin/x", "up to date"},
		{0, 0, "", "no upstream"},
		{2, 0, "origin/x", "2 ahead, 0 behind upstream"},
	} {
		if got := aheadBehindText(c.a, c.b, c.upstream); got != c.want {
			t.Errorf("aheadBehindText(%d,%d,%q) = %q, want %q", c.a, c.b, c.upstream, got, c.want)
		}
	}
}

func TestRepoChipTitleExplainsPartialInstances(t *testing.T) {
	c := repoChip(repoWith(repos.Ready, inventory.StateRunning, inventory.StatePartial, inventory.StateRunning))
	if c.Label != "2 running" || c.Title != "2 running, 1 partial" {
		t.Errorf("got %q / %q", c.Label, c.Title)
	}
	if c := repoChip(repoWith(repos.Ready, inventory.StateRunning)); c.Title != "" {
		t.Errorf("no partial, no title: %q", c.Title)
	}
}
