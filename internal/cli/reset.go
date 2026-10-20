package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Reset throws one environment away and starts it from zero: StartEnv with
// StartOptions.Reset adds the `down` step below and forces prepare and an
// image rebuild. The code of the checkout is never changed beyond the
// fast-forward the sync step already does (design.md of add-env-reset).

// ResetPreview is what a reset would do, for the confirmation of the CLI and
// of the dashboard. Nothing in it changes the checkout or the environment.
type ResetPreview struct {
	Project string   `json:"project"`
	Branch  string   `json:"branch"`
	Root    string   `json:"root"`
	Volumes []string `json:"volumes"`
	// Blocked: the checkout is behind its upstream and cannot be fast-forwarded
	// without touching local work; a reset would use the current code.
	Blocked bool   `json:"blocked"`
	Note    string `json:"note,omitempty"` // the sync line to show when Blocked
}

// PreviewReset fetches the upstream (changing no branch) and lists the Docker
// volumes of the checkout's instance, which `down --volumes` deletes.
func PreviewReset(checkout, newBase, legacyBase string) (ResetPreview, error) {
	toplevel, err := gitToplevel(checkout)
	if err != nil {
		return ResetPreview{}, fmt.Errorf("%s is not a git checkout", checkout)
	}
	root, err := identity.ResolvePath(toplevel)
	if err != nil {
		return ResetPreview{}, err
	}
	branch, err := currentBranch(root)
	if err != nil {
		return ResetPreview{}, err
	}
	project, _, err := checkoutInstance(root, branch, newBase, legacyBase)
	if err != nil {
		return ResetPreview{}, err
	}
	p := ResetPreview{Project: project, Branch: branch, Root: root, Volumes: []string{}}
	if res := syncRepo(root, 15*time.Second, false); res.Blocked {
		p.Blocked, p.Note = true, res.Line
	}
	// Docker's label filter is exact on the value, and the project name is unique to the instance.
	out, err := exec.Command("docker", "volume", "ls", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Name}}").Output()
	if err != nil {
		return p, fmt.Errorf("could not list the Docker volumes of %s: %w", project, err)
	}
	p.Volumes = append(p.Volumes, strings.Fields(string(out))...)
	sort.Strings(p.Volumes)
	return p, nil
}

// Describe is the text both confirmations show.
func (p ResetPreview) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reset the environment of %s (%s)?\n", p.Project, p.Branch)
	if len(p.Volumes) == 0 {
		b.WriteString("No Docker volumes exist for it; containers and images are rebuilt.")
	} else {
		b.WriteString("These Docker volumes and their data will be deleted:\n  " + strings.Join(p.Volumes, "\n  "))
	}
	b.WriteString("\nYour code is not touched.")
	if p.Blocked {
		b.WriteString("\nThe checkout is behind its upstream and has local work, so it cannot be updated: the reset will use the current code.")
	}
	return b.String()
}

// resetDown is the `down` step of a reset: containers, networks and volumes
// go, and the seeded marker is cleared so the data step runs on the new
// database. An instance that was never prepared has nothing to remove.
func resetDown(rec *runs.Run, root, branch, project, instanceDir, newBase, legacyBase string) error {
	st := rec.Step("down", "down", "remove the containers and delete the volumes")
	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err != nil {
		st.Skip("nothing prepared")
		return nil
	}
	w := st.Start()
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		st.End(err, 0)
		return err
	}
	defer lock.Release()
	fmt.Fprintf(w, "removing %s with its volumes\n", project)
	code, err := lifecycle.Execute(w, w, "down", root, branch, project, instanceDir, newBase, legacyBase, lifecycle.Options{Volumes: true})
	if err == nil && code != 0 {
		err = fmt.Errorf("down exited with status %d", code)
	}
	if err == nil {
		lifecycle.SetSeeded(instanceDir, nil)
	}
	st.End(err, code)
	return err
}

// resetPrepareArgs are the prepare args of a reset: the files and env file
// the instance was last prepared with, since that recipe is known to work,
// else the knowledge or adapter recipe like a first Start. Hostname, stable
// ports and secret references are kept by prepare itself, as in any re-prepare.
func resetPrepareArgs(root, main, instanceDir string) ([]string, error) {
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil && len(m.Files) > 0 {
		args := fileArgs(m.Files)
		if m.EnvFile != nil {
			args = append(args, "--env-file", *m.EnvFile)
		}
		return args, knowledge.CheckPrepareArgs(args)
	}
	if k, err := knowledge.LoadFirst(root, main); err != nil {
		return nil, err
	} else if k != nil {
		if args, err := knowledgePrepareArgs(k); args != nil || err != nil {
			return args, err
		}
	}
	return prepareArgs(root, main, instanceDir)
}

// resetStdin and resetIsTTY are the answers' source; tests replace them.
var (
	resetStdin io.Reader = os.Stdin
	resetIsTTY           = stdinIsTTY
)

func askYN(out io.Writer, in *bufio.Reader, question string) bool {
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	return strings.EqualFold(line, "y") || strings.EqualFold(line, "yes")
}

// cmdReset is `stackfork reset [--yes]` for the checkout in checkout.
func cmdReset(stdout, stderr io.Writer, subArgs []string, checkout, newBase, legacyBase string, global []string) (int, error) {
	fs := newFlagSet(stdout, "reset")
	yes := fs.Bool("yes", false, "delete without asking, and go on with the current code when it cannot be updated")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	if fs.NArg() > 0 {
		return 2, fmt.Errorf("unexpected argument %q (see `stackfork reset --help`)", fs.Arg(0))
	}
	pv, err := PreviewReset(checkout, newBase, legacyBase)
	if err != nil {
		return 0, err
	}
	current := *yes
	if !*yes {
		if !resetIsTTY() {
			return 2, errors.New("reset deletes this environment's data; pass --yes to confirm, or run it in a terminal")
		}
		in := bufio.NewReader(resetStdin)
		fmt.Fprintln(stdout, pv.Describe())
		if pv.Blocked {
			fmt.Fprintln(stdout, pv.Note)
		}
		if !askYN(stdout, in, "Delete and rebuild?") {
			fmt.Fprintln(stdout, "cancelled; nothing was changed")
			return 1, nil
		}
		if pv.Blocked {
			if !askYN(stdout, in, "Behind its upstream and blocked: reset with the current code?") {
				fmt.Fprintln(stdout, "cancelled; nothing was changed")
				return 1, nil
			}
			current = true
		}
	}
	profiles, err := resolveProfileDirs(nil)
	if err != nil {
		return 0, err
	}
	return 0, StartEnv(stdout, stderr, StartOptions{
		Checkout: pv.Root, NewStateBase: newBase, LegacyStateBase: legacyBase,
		Global: global, ProfileDirs: profiles, Reset: true, Current: current,
	})
}
