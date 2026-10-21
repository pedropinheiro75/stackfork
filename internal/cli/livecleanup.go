package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// cleanupDown is the `down` step of a branch cleanup (add-live-mode D19):
// `stackfork --instance P down --volumes` in this process. It runs after the
// worktree was removed, so the checkout comes from the instance's manifest,
// as in the orphan cleanup of `down --instance`. It stops host apps and
// containers and deletes the instance's volumes, database included.
func cleanupDown(newBase, legacyBase string) func(live.Target) func(io.Writer) error {
	return func(t live.Target) func(io.Writer) error {
		return func(w io.Writer) error {
			root, branch, instanceDir, err := resolveInstance(t.Project, newBase, legacyBase)
			if err != nil {
				return err
			}
			lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
			if err != nil {
				return err
			}
			defer lock.Release()
			fmt.Fprintf(w, "removing %s with its volumes\n", t.Project)
			code, err := lifecycle.Execute(w, w, "down", root, branch, t.Project, instanceDir, newBase, legacyBase, lifecycle.Options{Volumes: true})
			if err == nil && code != 0 {
				err = fmt.Errorf("down exited with status %d", code)
			}
			return err
		}
	}
}

// CleanupPreview is what a cleanup would delete, for the confirmation of the
// CLI and of the dashboard. Nothing in it changes anything.
type CleanupPreview struct {
	Repo         string   `json:"repo"`
	Path         string   `json:"path"`
	Branch       string   `json:"branch"`
	Project      string   `json:"project,omitempty"`
	PR           int      `json:"pr,omitempty"`
	Volumes      []string `json:"volumes"`
	Ignored      []string `json:"ignored,omitempty"`
	DeleteBranch bool     `json:"delete_branch"`
}

// projectVolumes lists the Docker volumes of one instance: the label filter is
// exact on the value, and the project name is unique to the instance.
func projectVolumes(project string) ([]string, error) {
	out, err := exec.Command("docker", "volume", "ls", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Name}}").Output()
	if err != nil {
		return nil, fmt.Errorf("could not list the Docker volumes of %s: %w", project, err)
	}
	v := strings.Fields(string(out))
	sort.Strings(v)
	return v, nil
}

// previewOf lists the volumes of the candidate's instance (an error when
// Docker cannot say: a person must not confirm a deletion they cannot read).
func previewOf(main string, c live.CandidateStatus, cl config.Cleanup) (CleanupPreview, error) {
	p := CleanupPreview{Repo: filepath.Base(main), Path: c.Path, Branch: c.Branch, Project: c.Project, PR: c.PR,
		Volumes: []string{}, Ignored: c.Ignored, DeleteBranch: cl.DeleteBranch}
	if c.Project != "" {
		v, err := projectVolumes(c.Project)
		if err != nil {
			return p, err
		}
		p.Volumes = v
	}
	return p, nil
}

// Describe is the text both confirmations show: the worktree, the instance and
// each volume, the branch and the ignored files that go with the worktree.
func (p CleanupPreview) Describe() string {
	var b strings.Builder
	pr := ""
	if p.PR != 0 {
		pr = fmt.Sprintf(" (#%d)", p.PR)
	}
	fmt.Fprintf(&b, "Clean up the merged branch %s of %s%s?\n\nThe worktree is deleted:\n  %s\n", p.Branch, p.Repo, pr, p.Path)
	switch {
	case p.Project == "":
		b.WriteString("\nIt has no instance, so no Docker data is deleted.\n")
	case len(p.Volumes) == 0:
		fmt.Fprintf(&b, "\nThe instance %s is removed. It has no Docker volumes.\n", p.Project)
	default:
		fmt.Fprintf(&b, "\nThe instance %s is removed with these Docker volumes and their data:\n  %s\n", p.Project, strings.Join(p.Volumes, "\n  "))
	}
	if p.DeleteBranch {
		fmt.Fprintf(&b, "\nThe local branch %s is deleted.\n", p.Branch)
	} else {
		fmt.Fprintf(&b, "\nThe local branch %s is kept.\n", p.Branch)
	}
	if len(p.Ignored) > 0 {
		b.WriteString("\nIgnored files that go with the worktree:\n  " + strings.Join(p.Ignored, "\n  ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// personScan is the scan that a person's command or click acts on. Live mode
// and cleanup must be on; ignored files are listed in the confirmation
// instead of blocking, because a person confirms.
func personScan(main, newBase, legacyBase string) (live.ScanInput, []live.CandidateStatus, config.Cleanup, error) {
	var in live.ScanInput
	cfg, err := config.Load()
	if err != nil {
		return in, nil, config.Cleanup{}, err
	}
	if !cfg.LiveFor(main) {
		return in, nil, config.Cleanup{}, fmt.Errorf("live mode is off for %s, so no branch is checked or cleaned; turn it on with stackfork live on", main)
	}
	cl := cfg.CleanupFor(main)
	if cl.Mode == config.CleanupOff {
		return in, nil, cl, fmt.Errorf("branch cleanup is off for %s; choose it with stackfork live cleanup suggest", main)
	}
	ts, err := liveTargets(newBase, legacyBase, main)
	if err != nil {
		return in, nil, cl, err
	}
	manual := cl
	manual.Mode = config.CleanupSuggest
	in = live.ScanInput{Main: main, NewBase: newBase, Settings: manual, Targets: ts, Now: time.Now()}
	cs, err := live.ScanCleanup(in)
	return in, cs, cl, err
}

// eligibleAt finds the candidate whose worktree is path (or whose branch is
// name, when byBranch) and says why it cannot be cleaned.
func eligibleAt(cs []live.CandidateStatus, key string, byBranch bool) (live.CandidateStatus, error) {
	real, _ := filepath.EvalSymlinks(key)
	i := slices.IndexFunc(cs, func(c live.CandidateStatus) bool {
		if byBranch {
			return c.Branch == key
		}
		return c.Path == key || (real != "" && c.Path == real)
	})
	if i < 0 {
		return live.CandidateStatus{}, fmt.Errorf("%s is not a merged or closed linked worktree (see stackfork live status)", key)
	}
	if c := cs[i]; !c.Eligible {
		return c, fmt.Errorf("%s: %s", c.Branch, c.Warning)
	}
	return cs[i], nil
}

// liveClean is `stackfork live clean [REPO] BRANCH [--yes]`: without the flag
// it prints what a cleanup would delete and deletes nothing; with it, it runs
// the D18 re-check and the cleanup run.
func liveClean(stdout io.Writer, main, branch string, yes bool, newBase, legacyBase string) (int, error) {
	in, cs, cl, err := personScan(main, newBase, legacyBase)
	if err != nil {
		return 1, err
	}
	c, err := eligibleAt(cs, branch, true)
	if err != nil {
		return 1, fmt.Errorf("%w; nothing was deleted", err)
	}
	if !yes {
		pv, err := previewOf(main, c, cl)
		if err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, pv.Describe())
		fmt.Fprintf(stdout, "\nNothing was deleted. Run it with --yes to clean up: stackfork live clean %s %s --yes\n", main, branch)
		return 0, nil
	}
	job := in.Job(c, false)
	job.Settings = cl
	if job.Instance != nil {
		job.Down = cleanupDown(newBase, legacyBase)(*job.Instance)
	}
	job.Out = stdout
	if out := live.RunCleanup(context.Background(), job); out.Err != nil {
		return 1, fmt.Errorf("cleanup of %s failed: %w", branch, out.Err)
	}
	fmt.Fprintf(stdout, "cleaned up %s\n", branch)
	return 0, nil
}

// dashboardCleanup is what POST /api/live's cleanup ops run.
func dashboardCleanup(newBase, legacyBase string) *dashboard.LiveCleanup {
	return &dashboard.LiveCleanup{
		CleanText: func(main, path string) (string, error) {
			_, cs, cl, err := personScan(main, newBase, legacyBase)
			if err != nil {
				return "", err
			}
			c, err := eligibleAt(cs, path, false)
			if err != nil {
				return "", err
			}
			pv, err := previewOf(main, c, cl)
			return pv.Describe(), err
		},
		Clean: func(out io.Writer, main, path string, locked func(string) bool) error {
			in, cs, cl, err := personScan(main, newBase, legacyBase)
			if err != nil {
				return err
			}
			c, err := eligibleAt(cs, path, false)
			if err != nil {
				return err
			}
			job := in.Job(c, false)
			job.Settings, job.ActionLocked, job.Out = cl, locked, out
			if job.Instance != nil {
				job.Down = cleanupDown(newBase, legacyBase)(*job.Instance)
			}
			return live.RunCleanup(context.Background(), job).Err
		},
		Keep: func(main, path string) error {
			_, cs, _, err := personScan(main, newBase, legacyBase)
			if err != nil {
				return err
			}
			real, _ := filepath.EvalSymlinks(path)
			i := slices.IndexFunc(cs, func(c live.CandidateStatus) bool { return c.Path == path || c.Path == real })
			if i < 0 {
				return fmt.Errorf("%s is not a merged or closed linked worktree", path)
			}
			return live.KeepBranch(newBase, main, cs[i].Branch, cs[i].Head)
		},
		SettingText: func(main string, cl config.Cleanup) (string, error) {
			var b strings.Builder
			name := filepath.Base(main)
			if cl.Mode == config.CleanupAuto {
				fmt.Fprintf(&b, "Clean up merged branches of %s automatically?\n\nWorktrees of merged branches and the databases of their instances are deleted %d hours after a branch is found merged, without asking.", name, cl.GraceHours)
			} else {
				fmt.Fprintf(&b, "Delete the local branch after a cleanup in %s?", name)
			}
			if cl.DeleteBranch {
				b.WriteString("\nThe local branch of a cleaned worktree is deleted too.")
			} else {
				b.WriteString("\nLocal branches are kept.")
			}
			if cl.Mode != config.CleanupAuto {
				return b.String(), nil
			}
			in := live.ScanInput{Main: main, NewBase: newBase, Settings: cl, Now: time.Now()}
			var err error
			if in.Targets, err = liveTargets(newBase, legacyBase, main); err != nil {
				return "", err
			}
			cs, err := live.ScanCleanup(in)
			if err != nil {
				return "", err
			}
			var today []string
			for _, c := range cs {
				if c.State != "merged" || !c.Eligible || c.Kept {
					continue
				}
				pv, err := previewOf(main, c, cl)
				if err != nil {
					return "", err
				}
				line := pv.Path
				if len(pv.Volumes) > 0 {
					line += " (volumes " + strings.Join(pv.Volumes, ", ") + ")"
				}
				today = append(today, line)
			}
			if len(today) == 0 {
				b.WriteString("\n\nNo worktree would be deleted today.")
			} else {
				b.WriteString("\n\nThese would be deleted today, once the grace period ends:\n  " + strings.Join(today, "\n  "))
			}
			return b.String(), nil
		},
	}
}
