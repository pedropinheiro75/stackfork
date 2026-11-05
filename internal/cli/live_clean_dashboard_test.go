package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/live"
)

func TestDashboardCleanupTextNamesWhatIsDeleted(t *testing.T) {
	f := newCleanupCLIFx(t)
	liveOnFor(t, f.Main, &config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true})
	t.Setenv("DOCKER_FAKE_VOLUMES", "acme-web-feature-login_db-data\n")
	ops := dashboardCleanup(f.State, f.Legacy)
	text, _, err := ops.CleanText(f.Main, f.WT)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{f.WT, "acme-web-feature-login_db-data", "The local branch feature-login is deleted."} {
		if !strings.Contains(text, want) {
			t.Errorf("the dialog misses %q:\n%s", want, text)
		}
	}
	// ignored files that go with the worktree are named too
	ignorePath := filepath.FromSlash(strings.TrimSpace(gitT(t, f.WT, "rev-parse", "--git-path", "info/exclude"))) // C:/x on Windows
	if !filepath.IsAbs(ignorePath) {
		ignorePath = filepath.Join(f.WT, ignorePath)
	}
	os.MkdirAll(filepath.Dir(ignorePath), 0o755)
	os.WriteFile(ignorePath, []byte("/.env.local\n"), 0o644)
	os.WriteFile(f.WT+"/.env.local", []byte("x"), 0o644)
	if text, _, _ = ops.CleanText(f.Main, f.WT); !strings.Contains(text, ".env.local") {
		t.Errorf("the ignored file is not named:\n%s", text)
	}
	os.WriteFile(f.WT+"/login.txt", []byte("edited\n"), 0o644)
	if _, _, err := ops.CleanText(f.Main, f.WT); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("an edited worktree gets no dialog: %v", err)
	}
	if _, _, err := ops.CleanText(f.Main, "/tmp"); err == nil {
		t.Fatal("a path that is not a candidate gets no dialog")
	}
}

func TestDashboardKeepThenSettingTextAndClean(t *testing.T) {
	f := newCleanupCLIFx(t)
	liveOnFor(t, f.Main, &config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24})
	t.Setenv("DOCKER_FAKE_VOLUMES", "acme-web-feature-login_db-data\n")
	ops := dashboardCleanup(f.State, f.Legacy)

	auto := config.Cleanup{Mode: config.CleanupAuto, GraceHours: 12, DeleteBranch: true}
	text, err := ops.SettingText(f.Main, auto)
	if err != nil || !strings.Contains(text, "12 hours") || !strings.Contains(text, f.WT) || !strings.Contains(text, "acme-web-feature-login_db-data") ||
		!strings.Contains(text, "without asking") || !strings.Contains(text, "local branch") {
		t.Fatalf("%v\n%s", err, text)
	}

	if err := ops.Keep(f.Main, f.WT); err != nil {
		t.Fatal(err)
	}
	st, _ := live.LoadCleanupState(f.State, f.Main)
	if len(st.Keep) != 1 || st.Keep[0].Branch != "feature-login" {
		t.Fatalf("keep: %+v", st.Keep)
	}
	if text, _ = ops.SettingText(f.Main, auto); !strings.Contains(text, "No worktree would be deleted today") {
		t.Fatalf("a kept branch is not deleted today:\n%s", text)
	}
	if text, _ = ops.SettingText(f.Main, config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true}); strings.Contains(text, f.WT) {
		t.Fatalf("branch deletion alone lists no worktrees:\n%s", text)
	}

	// a person asked for it explicitly: Keep does not stop a cleanup they confirm,
	// and a held dashboard action lock does
	if err := ops.Clean(io.Discard, f.Main, f.WT, dashboard.CleanConfirmed{Volumes: []string{"acme-web-feature-login_db-data"}}, func(string) bool { return true }); err == nil || !strings.Contains(err.Error(), "action is running") {
		t.Fatalf("held lock: %v", err)
	}
	if _, err := os.Stat(f.WT); err != nil {
		t.Fatal("the worktree went away under a held action lock")
	}
	if err := ops.Clean(io.Discard, f.Main, f.WT, dashboard.CleanConfirmed{Volumes: []string{"acme-web-feature-login_db-data"}}, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.WT); err == nil {
		t.Fatal("the worktree is still there")
	}
}

// The cleanup deletes no more than the dialog named: ignored files or volumes
// that appeared between the confirmation and the click stop it.
func TestDashboardCleanRefusesWhatGrewSinceTheConfirmation(t *testing.T) {
	f := newCleanupCLIFx(t)
	liveOnFor(t, f.Main, &config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24})
	t.Setenv("DOCKER_FAKE_VOLUMES", "acme-web-feature-login_db-data\n")
	ops := dashboardCleanup(f.State, f.Legacy)
	_, confirmed, err := ops.CleanText(f.Main, f.WT)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_FAKE_VOLUMES", "acme-web-feature-login_db-data\nacme-web-feature-login_new\n")
	if err := ops.Clean(io.Discard, f.Main, f.WT, confirmed, func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "more Docker volumes") {
		t.Fatalf("volumes grew: %v", err)
	}
	t.Setenv("DOCKER_FAKE_VOLUMES", "acme-web-feature-login_db-data\n")
	ignorePath := filepath.FromSlash(strings.TrimSpace(gitT(t, f.WT, "rev-parse", "--git-path", "info/exclude"))) // C:/x on Windows
	if !filepath.IsAbs(ignorePath) {
		ignorePath = filepath.Join(f.WT, ignorePath)
	}
	os.MkdirAll(filepath.Dir(ignorePath), 0o755)
	os.WriteFile(ignorePath, []byte("/.env.local\n"), 0o644)
	os.WriteFile(f.WT+"/.env.local", []byte("x"), 0o644)
	if err := ops.Clean(io.Discard, f.Main, f.WT, confirmed, func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "more ignored files") {
		t.Fatalf("ignored files grew: %v", err)
	}
	if _, err := os.Stat(f.WT); err != nil {
		t.Fatal("the worktree was removed")
	}
}
