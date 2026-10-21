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

// liveClean is `stackfork live clean [REPO] BRANCH [--yes]`: without --yes it
// prints what a cleanup would delete and deletes nothing; with --yes it runs
// the D18 re-check and the cleanup run.
func liveClean(stdout io.Writer, main, branch string, yes bool, newBase, legacyBase string) (int, error) {
	cfg, err := config.Load()
	if err != nil {
		return 0, err
	}
	if !cfg.LiveFor(main) {
		return 1, fmt.Errorf("live mode is off for %s, so no branch is checked or cleaned; turn it on with stackfork live on", main)
	}
	cl := cfg.CleanupFor(main)
	if cl.Mode == config.CleanupOff {
		return 1, fmt.Errorf("branch cleanup is off for %s; choose it with stackfork live cleanup suggest", main)
	}
	ts, err := liveTargets(newBase, legacyBase, main)
	if err != nil {
		return 0, err
	}
	manual := cl
	manual.Mode = config.CleanupSuggest // a person confirms: ignored files are listed, not a block
	in := live.ScanInput{Main: main, NewBase: newBase, Settings: manual, Targets: ts, Now: time.Now()}
	cs, err := live.ScanCleanup(in)
	if err != nil {
		return 0, err
	}
	i := slices.IndexFunc(cs, func(c live.CandidateStatus) bool { return c.Branch == branch })
	if i < 0 {
		return 1, fmt.Errorf("%s is not a merged or closed linked worktree of %s (see stackfork live status)", branch, filepath.Base(main))
	}
	c := cs[i]
	if !c.Eligible {
		return 1, fmt.Errorf("%s: %s; nothing was deleted", branch, c.Warning)
	}
	pv, err := previewOf(main, c, cl)
	if err != nil {
		return 1, err
	}
	if !yes {
		fmt.Fprintln(stdout, pv.Describe())
		fmt.Fprintf(stdout, "\nNothing was deleted. Run it with --yes to clean up: stackfork live clean %s %s --yes\n", main, branch)
		return 0, nil
	}
	job := in.Job(c, false)
	if job.Instance != nil {
		job.Down = cleanupDown(newBase, legacyBase)(*job.Instance)
	}
	job.Out = stdout
	out := live.RunCleanup(context.Background(), job)
	if out.Err != nil {
		return 1, fmt.Errorf("cleanup of %s failed: %w", branch, out.Err)
	}
	fmt.Fprintf(stdout, "cleaned up %s\n", branch)
	return 0, nil
}
