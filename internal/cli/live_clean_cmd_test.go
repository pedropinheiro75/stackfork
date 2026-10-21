package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func liveOnFor(t *testing.T, main string, cl *config.Cleanup) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetLive(main); err != nil {
		t.Fatal(err)
	}
	if cl != nil {
		if err := cfg.SetCleanup(main, *cl); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func (f *cleanupCLIFx) sf(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runCLI(t, append([]string{"--state-dir", f.State, "--legacy-state-dir", f.Legacy}, args...)...)
}

func TestLiveCleanPreviewDeletesNothing(t *testing.T) {
	f := newCleanupCLIFx(t)
	liveOnFor(t, f.Main, &config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true})
	t.Setenv("DOCKER_FAKE_VOLUMES", "acme-web-feature-login_db-data\nacme-web-feature-login_cache\n")
	off := fileSize(f.Log)
	out, e, code := f.sf(t, "live", "clean", f.Main, "feature-login")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, e)
	}
	for _, want := range []string{"The worktree is deleted:", f.WT, "acme-web-feature-login_db-data", "acme-web-feature-login_cache",
		"The local branch feature-login is deleted.", "Nothing was deleted", "--yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview misses %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(f.WT); err != nil {
		t.Fatal("the preview removed the worktree")
	}
	if gitT(t, f.Main, "branch", "--list", "feature-login") == "" {
		t.Fatal("the preview removed the branch")
	}
	d := dockerAfter(t, f.Log, off)
	if strings.Contains(d, " down ") || strings.Contains(d, " rm ") {
		t.Fatalf("the preview called docker to delete:\n%s", d)
	}
}

func TestLiveCleanYesCleansAndRecordsTheRun(t *testing.T) {
	f := newCleanupCLIFx(t)
	liveOnFor(t, f.Main, &config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24})
	out, e, code := f.sf(t, "--root", f.Main, "live", "clean", "feature-login", "--yes") // one positional: the branch
	if code != 0 || !strings.Contains(out, "cleaned up feature-login") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	if _, err := os.Stat(f.WT); err == nil {
		t.Fatal("the worktree is still there")
	}
	if gitT(t, f.Main, "branch", "--list", "feature-login") == "" {
		t.Fatal("delete_branch is off: the branch must stay")
	}
	dirs, _ := filepath.Glob(filepath.Join(f.State, "*", "runs", "*.json"))
	var cleanups int
	for _, p := range dirs {
		var r runs.Run
		b, _ := os.ReadFile(p)
		json.Unmarshal(b, &r)
		if r.Cause != nil && r.Cause.Cleanup != nil {
			cleanups++
		}
	}
	if cleanups != 1 {
		t.Fatalf("%d cleanup runs", cleanups)
	}
}

func TestLiveCleanRefusals(t *testing.T) {
	f := newCleanupCLIFx(t)
	// live mode off
	if _, e, code := f.sf(t, "live", "clean", f.Main, "feature-login", "--yes"); code == 0 || !strings.Contains(e, "live mode is off") {
		t.Fatalf("live off: %d %s", code, e)
	}
	liveOnFor(t, f.Main, &config.Cleanup{Mode: config.CleanupOff, GraceHours: 24})
	if _, e, code := f.sf(t, "live", "clean", f.Main, "feature-login", "--yes"); code == 0 || !strings.Contains(e, "cleanup is off") {
		t.Fatalf("cleanup off: %d %s", code, e)
	}
	liveOnFor(t, f.Main, &config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24})
	if _, e, code := f.sf(t, "live", "clean", f.Main, "no-such-branch"); code == 0 || !strings.Contains(e, "not a merged or closed linked worktree") {
		t.Fatalf("unknown branch: %d %s", code, e)
	}
	os.WriteFile(filepath.Join(f.WT, "login.txt"), []byte("edited\n"), 0o644)
	off := fileSize(f.Log)
	if _, e, code := f.sf(t, "live", "clean", f.Main, "feature-login", "--yes"); code == 0 || !strings.Contains(e, "PR merged, but this worktree has uncommitted changes") || !strings.Contains(e, "nothing was deleted") {
		t.Fatalf("dirty: %d %s", code, e)
	}
	if d := dockerAfter(t, f.Log, off); strings.Contains(d, " down ") || strings.Contains(d, " rm ") {
		t.Fatalf("docker deleted something:\n%s", d)
	}
	if _, err := os.Stat(f.WT); err != nil {
		t.Fatal("a dirty worktree must not be touched")
	}
	if _, e, code := f.sf(t, "live", "clean"); code == 0 || !strings.Contains(e, "usage") {
		t.Fatalf("usage: %d %s", code, e)
	}
}

func TestLiveStatusListsCandidates(t *testing.T) {
	f := newCleanupCLIFx(t)
	liveOnFor(t, f.Main, nil)
	out, e, code := f.sf(t, "live", "status", f.Main)
	if code != 0 || !strings.Contains(out, "feature-login: merged, clean up with: stackfork live clean "+f.Main+" feature-login") {
		t.Fatalf("%d %s %s", code, out, e)
	}
	out, _, _ = f.sf(t, "live", "status", f.Main, "--json")
	var rows []struct {
		Candidates []struct {
			Branch, State, Project string
			Eligible               bool
		}
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || len(rows[0].Candidates) != 1 ||
		rows[0].Candidates[0].Branch != "feature-login" || rows[0].Candidates[0].State != "merged" || !rows[0].Candidates[0].Eligible || rows[0].Candidates[0].Project == "" {
		t.Fatalf("%v %s", err, out)
	}
	os.WriteFile(filepath.Join(f.WT, "login.txt"), []byte("edited\n"), 0o644)
	out, _, _ = f.sf(t, "live", "status", f.Main)
	if !strings.Contains(out, "feature-login: merged, PR merged, but this worktree has uncommitted changes") {
		t.Fatalf("%s", out)
	}
}
