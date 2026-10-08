package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// reposCacheFile is where the scan cache lives (design.md D2).
func reposCacheFile(newStateBase string) string {
	return filepath.Join(newStateBase, "repos-cache.json")
}

// scanRepos runs one scan against the configured roots, the way both
// `stackfork repos` and the dashboard's first load do.
func scanRepos(newStateBase, legacyStateBase string, rescan bool) (repos.Result, error) {
	cfg, err := config.Load()
	if err != nil {
		return repos.Result{}, err
	}
	var insts []inventory.Instance
	if len(cfg.Roots) > 0 {
		inv, err := inventory.Collect(inventory.Options{NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, Version: Version})
		if err != nil {
			return repos.Result{}, err
		}
		insts = inv.Instances
	}
	profiles, err := resolveProfileDirs(nil)
	if err != nil {
		return repos.Result{}, err
	}
	return repos.Scan(repos.Options{
		Config: cfg, Instances: insts, ProfileDirs: profiles,
		CacheFile: reposCacheFile(newStateBase), Rescan: rescan,
	}), nil
}

// cmdRepos is `stackfork repos [--json] [--rescan] [--prompt REPO BRANCH]`.
func cmdRepos(stdout io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	fs := newFlagSet(stdout, "repos")
	jsonFlag := fs.Bool("json", false, "")
	rescanFlag := fs.Bool("rescan", false, "")
	promptFlag := fs.Bool("prompt", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	res, err := scanRepos(newStateBase, legacyStateBase, *rescanFlag)
	if err != nil {
		return 0, err
	}
	if *promptFlag {
		if fs.NArg() != 2 {
			return 2, errors.New("repos --prompt takes REPO BRANCH")
		}
		r, err := repos.FindRepo(res, fs.Arg(0))
		if err != nil {
			return 0, err
		}
		fmt.Fprintln(stdout, repos.Prompt(r.Name, r.Path, fs.Arg(1)))
		return 0, nil
	}
	if *jsonFlag {
		_, err := stdout.Write(repos.Marshal(res))
		return 0, err
	}
	if !res.Configured {
		fmt.Fprintln(stdout, "No scan roots configured. Add one with: stackfork config add-root PATH")
		return 0, nil
	}
	w := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "GROUP\tREPO\tREADINESS\tRUNNING\tWORKTREES\tBRANCHES\tLAST COMMIT")
	for _, r := range res.Repos {
		last := "-"
		if !r.LastCommit.IsZero() {
			last = r.LastCommit.Format("2006-01-02")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n", r.Group, r.Name, r.Readiness, r.Running, len(r.Worktrees), len(r.Branches), last)
	}
	if len(res.Repos) == 0 {
		fmt.Fprintln(w, "(no repos found under the configured roots)")
	}
	return 0, w.Flush()
}
