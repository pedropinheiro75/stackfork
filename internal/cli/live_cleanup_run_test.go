package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cleanup"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// cleanupCLIFx is a repository with a bare origin, a started instance in the
// worktree of feature-login (fake Docker), and that branch squash-merged into
// origin/main and deleted there. Everything lives under temp dirs.
type cleanupCLIFx struct {
	Main, WT, Origin, State, Legacy, Log string
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@e.x", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newCleanupCLIFx(t *testing.T) *cleanupCLIFx {
	t.Helper()
	root, state := reposEnv(t)
	f := &cleanupCLIFx{State: state, Legacy: t.TempDir()}
	f.Log = installFakeDocker(t)
	steps := []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", ".agent-dev/app.compose.yaml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
	}
	f.Main = planRepo(t, root, steps)
	f.Origin = filepath.Join(root, "origin.git")
	gitT(t, root, "init", "-q", "--bare", "-b", "main", f.Origin)
	gitT(t, f.Main, "add", "-A")
	gitT(t, f.Main, "commit", "-q", "-m", "plan")
	gitT(t, f.Main, "remote", "add", "origin", f.Origin)
	gitT(t, f.Main, "push", "-q", "-u", "origin", "main")
	wt, err := repos.AddWorktree(f.Main, "feature-login", "main")
	if err != nil {
		t.Fatal(err)
	}
	f.WT = wt.Path
	if err := os.WriteFile(filepath.Join(f.WT, "login.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.WT, "add", "-A")
	gitT(t, f.WT, "commit", "-q", "-m", "login")
	gitT(t, f.WT, "push", "-q", "-u", "origin", "feature-login")
	if out, e, code := runCLI(t, "--state-dir", state, "--legacy-state-dir", f.Legacy, "start-env", f.WT); code != 0 {
		t.Fatalf("start-env: %s %s", out, e)
	}
	os.WriteFile(filepath.Join(f.Main, "other.txt"), []byte("o\n"), 0o644)
	gitT(t, f.Main, "add", "-A")
	gitT(t, f.Main, "commit", "-q", "-m", "unrelated")
	gitT(t, f.Main, "merge", "-q", "--squash", "feature-login")
	gitT(t, f.Main, "commit", "-q", "-m", "feature-login (#42)")
	gitT(t, f.Main, "push", "-q", "origin", "main")
	gitT(t, f.Main, "push", "-q", "origin", "--delete", "feature-login")
	if err := cleanup.Fetch(f.Main, "origin", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *cleanupCLIFx) job(t *testing.T, cl config.Cleanup, auto bool) live.CleanupJob {
	t.Helper()
	ts, err := live.Targets(f.State, f.Legacy, f.Main, Version)
	if err != nil {
		t.Fatal(err)
	}
	in := live.ScanInput{Main: f.Main, NewBase: f.State, Settings: cl, Targets: ts, Now: time.Now()}
	cs, err := live.ScanCleanup(in)
	if err != nil || len(cs) != 1 {
		t.Fatalf("scan: %+v %v", cs, err)
	}
	j := in.Job(cs[0], auto)
	if j.Instance == nil {
		t.Fatalf("no instance for %s in %+v", cs[0].Path, ts)
	}
	j.Down = cleanupDown(f.State, f.Legacy)(*j.Instance)
	return j
}

func TestBranchCleanupRunsRemoveWorktreeDownAndDeleteBranch(t *testing.T) {
	f := newCleanupCLIFx(t)
	j := f.job(t, config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true}, false)
	off := fileSize(f.Log)
	out := live.RunCleanup(context.Background(), j)
	if out.Err != nil || out.Run.Outcome != runs.Succeeded {
		t.Fatalf("%+v", out)
	}
	r, err := runs.Get(j.Instance.InstanceDir, out.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, s := range r.Steps {
		steps = append(steps, s.ID+"="+s.State)
	}
	if strings.Join(steps, " ") != "remove-worktree=succeeded down=succeeded delete-branch=succeeded" || r.Command != "live" || r.Trigger != "live" || r.Cause.Cleanup.Branch != "feature-login" {
		t.Fatalf("%s/%s %v %+v", r.Command, r.Trigger, steps, r.Cause)
	}
	// --volumes appears in the docker argument list of this run kind
	var downs int
	for _, line := range strings.Split(dockerAfter(t, f.Log, off), "\n") {
		if strings.Contains(line, " down ") {
			downs++
			if !strings.Contains(line, "--volumes") || !strings.Contains(line, j.Instance.Project) {
				t.Errorf("down without --volumes or for another project: %s", line)
			}
		}
	}
	if downs != 1 {
		t.Fatalf("%d down calls", downs)
	}
	if _, err := os.Stat(f.WT); err == nil {
		t.Error("the worktree is still there")
	}
	if gitT(t, f.Main, "branch", "--list", "feature-login") != "" {
		t.Error("the branch is still there")
	}
}

// A live reconcile run never passes --volumes; only the cleanup run does.
func TestOrdinaryLiveRunsStillNeverPassDownOrVolumes(t *testing.T) {
	r, tg, _, log := reconcileFixture(t)
	off := fileSize(log)
	if o, err := r.Reconcile(context.Background(), tg); err != nil || o.Status != live.Ran {
		t.Fatalf("%+v %v", o, err)
	}
	assertNoForbiddenArgs(t, dockerAfter(t, log, off))
}

func TestBranchCleanupDirtyFileFailsAtRemoveWorktreeWithNoDockerCall(t *testing.T) {
	f := newCleanupCLIFx(t)
	j := f.job(t, config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24, DeleteBranch: true}, false)
	off := fileSize(f.Log)
	j.ActionLocked = func(string) bool { // runs after the status read of the re-check
		os.WriteFile(filepath.Join(f.WT, "login.txt"), []byte("edited\n"), 0o644)
		return false
	}
	out := live.RunCleanup(context.Background(), j)
	if out.Err == nil || out.Run.Step != "remove-worktree" || out.Run.Outcome != runs.Failed {
		t.Fatalf("%+v", out)
	}
	if d := dockerAfter(t, f.Log, off); d != "" {
		t.Fatalf("docker was called:\n%s", d)
	}
	if b, err := os.ReadFile(filepath.Join(f.WT, "login.txt")); err != nil || string(b) != "edited\n" {
		t.Fatal("the worktree was touched")
	}
	if p, _ := live.ReadPause(f.State, f.Main); p == nil || p.Step != "remove-worktree" {
		t.Fatalf("pause: %+v", p)
	}
}

func TestBranchCleanupFailedDownLeavesAnOrphanInstance(t *testing.T) {
	f := newCleanupCLIFx(t)
	j := f.job(t, config.Cleanup{Mode: config.CleanupSuggest, GraceHours: 24}, false)
	t.Setenv("DOCKER_FAKE_EXIT", "1")
	out := live.RunCleanup(context.Background(), j)
	if out.Err == nil || out.Run.Step != "down" || errors.Is(out.Err, live.ErrNotEligible) {
		t.Fatalf("%+v", out)
	}
	if _, err := os.Stat(f.WT); err == nil {
		t.Error("the worktree must be gone: it was removed first")
	}
}
