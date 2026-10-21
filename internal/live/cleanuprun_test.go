package live

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

func TestRunCleanupFullCleanupRecordsThreeSucceededSteps(t *testing.T) {
	f := newCleanupFx(t)
	j := f.job(t)
	j.Settings = config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true}
	j.Result.PR = 42
	downs := 0
	j.Down = func(w io.Writer) error {
		downs++
		if _, err := os.Stat(f.WT); err == nil {
			t.Error("down ran before the worktree was removed")
		}
		return nil
	}
	out := RunCleanup(context.Background(), j)
	if out.Err != nil || out.Run.ID == 0 || out.Run.Outcome != runs.Succeeded || downs != 1 {
		t.Fatalf("%+v downs=%d", out, downs)
	}
	r, err := runs.Get(f.Inst.InstanceDir, out.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, s := range r.Steps {
		steps = append(steps, s.ID+"="+s.State)
	}
	if got := strings.Join(steps, " "); got != "remove-worktree=succeeded down=succeeded delete-branch=succeeded" {
		t.Fatalf("steps: %s", got)
	}
	c := r.Cause.Cleanup
	if r.Command != "live" || r.Trigger != "live" || c == nil || c.Branch != "feature-login" || c.Result != "merged" || c.PR != 42 || c.Via != "git+gh" {
		t.Fatalf("%s/%s cause %+v", r.Command, r.Trigger, r.Cause)
	}
	if _, err := os.Stat(f.WT); err == nil {
		t.Error("the worktree is still there")
	}
	if git(t, f.Main, "branch", "--list", "feature-login") != "" {
		t.Error("the branch is still there")
	}
	if p, _ := ReadPause(f.State, f.Main); p != nil {
		t.Errorf("a succeeded cleanup paused live mode: %+v", p)
	}
	st, _ := LoadCleanupState(f.State, f.Main)
	if len(st.History) != 1 || st.History[0].Result != "cleaned" || st.History[0].Run != r.ID {
		t.Fatalf("history: %+v", st.History)
	}
}

func TestRunCleanupKeepsTheBranchWhenTheSettingSaysSo(t *testing.T) {
	f := newCleanupFx(t)
	j := f.job(t)
	j.Settings = config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24}
	out := RunCleanup(context.Background(), j)
	r, _ := runs.Get(f.Inst.InstanceDir, out.Run.ID)
	if out.Err != nil || len(r.Steps) != 2 {
		t.Fatalf("%+v steps %d", out, len(r.Steps))
	}
	if git(t, f.Main, "branch", "--list", "feature-login") == "" {
		t.Error("the branch must stay when delete_branch is off")
	}
}

func TestRunCleanupEditAfterTheCheckFailsAtRemoveWorktreeWithNoDockerCall(t *testing.T) {
	f := newCleanupFx(t)
	j := f.job(t)
	j.Settings = config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true}
	downs := 0
	j.Down = func(io.Writer) error { downs++; return nil }
	// ActionLocked runs in the last check of the re-check: this edit lands after the status read.
	j.ActionLocked = func(string) bool {
		write(t, f.WT, "login.txt", "edited after the check\n")
		return false
	}
	out := RunCleanup(context.Background(), j)
	if out.Err == nil || out.Run.Outcome != runs.Failed || out.Run.Step != "remove-worktree" || downs != 0 {
		t.Fatalf("%+v downs=%d", out, downs)
	}
	if _, err := os.Stat(filepath.Join(f.WT, "login.txt")); err != nil {
		t.Fatal("the edited worktree was deleted")
	}
	r, _ := runs.Get(f.Inst.InstanceDir, out.Run.ID)
	if r.Steps[0].State != runs.Failed || r.Steps[1].State != runs.Pending {
		t.Fatalf("steps: %+v %+v", r.Steps[0], r.Steps[1])
	}
	p, _ := ReadPause(f.State, f.Main)
	if p == nil || p.Kind != PauseFailed || p.Step != "remove-worktree" || p.Run != r.ID {
		t.Fatalf("pause: %+v", p)
	}
	if git(t, f.Main, "branch", "--list", "feature-login") == "" {
		t.Error("the branch must stay after a failed removal")
	}
	st, _ := LoadCleanupState(f.State, f.Main)
	if len(st.History) != 1 || st.History[0].Result != "failed" {
		t.Fatalf("history: %+v", st.History)
	}
}

func TestRunCleanupRefusesBeforeAnyStepWhenTheRecheckFails(t *testing.T) {
	f := newCleanupFx(t)
	j := f.job(t)
	write(t, f.WT, "login.txt", "edited\n")
	downs := 0
	j.Down = func(io.Writer) error { downs++; return nil }
	out := RunCleanup(context.Background(), j)
	if !errors.Is(out.Err, ErrNotEligible) || !strings.Contains(out.Err.Error(), "uncommitted changes") || out.Run.ID != 0 || downs != 0 {
		t.Fatalf("%+v", out)
	}
	if list, _ := runs.List(f.Inst.InstanceDir); len(list) != 0 {
		t.Fatalf("a refused cleanup recorded %d runs", len(list))
	}
	if p, _ := ReadPause(f.State, f.Main); p != nil {
		t.Fatalf("a refusal must not pause: %+v", p)
	}
}

func TestRunCleanupHeldRunLockRefuses(t *testing.T) {
	f := newCleanupFx(t)
	j := f.job(t)
	held, err := runs.Begin(f.Inst.InstanceDir, runs.Meta{Command: "up", Trigger: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Finish(nil)
	out := RunCleanup(context.Background(), j)
	if !errors.Is(out.Err, ErrNotEligible) || !strings.Contains(out.Err.Error(), "run lock") {
		t.Fatalf("%+v", out)
	}
	if _, err := os.Stat(f.WT); err != nil {
		t.Fatal("the worktree was removed under a held run lock")
	}
}

func TestRunCleanupFailedDownLeavesTheOrphanRetry(t *testing.T) {
	f := newCleanupFx(t)
	j := f.job(t)
	j.Down = func(io.Writer) error { return errors.New("docker is not running") }
	out := RunCleanup(context.Background(), j)
	if out.Err == nil || out.Run.Step != "down" || out.Pause == nil {
		t.Fatalf("%+v", out)
	}
	r, _ := runs.Get(f.Inst.InstanceDir, out.Run.ID)
	if r.Retry != "stackfork --instance acme-web-feature-login down --volumes" || r.Steps[0].State != runs.Succeeded {
		t.Fatalf("retry %q steps %+v", r.Retry, r.Steps)
	}
}

func TestRunCleanupWithoutAnInstanceRecordsNoRun(t *testing.T) {
	f := newCleanupFx(t)
	j := f.job(t)
	j.Instance, j.Down = nil, nil
	j.Settings = config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true}
	out := RunCleanup(context.Background(), j)
	if out.Err != nil || out.Run.ID != 0 {
		t.Fatalf("%+v", out)
	}
	if _, err := os.Stat(f.Inst.InstanceDir + "/runs"); err == nil {
		t.Error("a run directory was created for a candidate without an instance")
	}
	if _, err := os.Stat(f.WT); err == nil {
		t.Error("the worktree is still there")
	}
	st, _ := LoadCleanupState(f.State, f.Main)
	if len(st.History) != 1 || st.History[0].Branch != "feature-login" || st.History[0].Run != 0 || st.History[0].Result != "cleaned" {
		t.Fatalf("history: %+v", st.History)
	}
}

func TestCleanupHistoryKeepsTheLast20(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		RecordCleanup(dir, "/r", HistoryEvent{Branch: "b", Result: "cleaned", Run: i})
	}
	st, _ := LoadCleanupState(dir, "/r")
	if len(st.History) != 20 || st.History[0].Run != 5 {
		t.Fatalf("%d events, first run %d", len(st.History), st.History[0].Run)
	}
}
