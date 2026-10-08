package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// resolveRepoArg turns a repo path or a scanned repo name into a path.
func resolveRepoArg(arg, newStateBase, legacyStateBase string) (string, error) {
	isPath := strings.ContainsAny(arg, `/\`) || arg == "." || arg == ".." || strings.HasPrefix(arg, "~")
	if !isPath {
		res, err := scanRepos(newStateBase, legacyStateBase, false)
		if err != nil {
			return "", err
		}
		r, ferr := repos.FindRepo(res, arg)
		if ferr == nil {
			return r.Path, nil
		}
		if fi, err := os.Stat(arg); err != nil || !fi.IsDir() {
			return "", ferr
		}
	}
	p, err := config.ExpandHome(arg)
	if err != nil {
		return "", err
	}
	return identity.ResolvePath(p)
}

// cmdWorktree is `stackfork worktree add REPO BRANCH [--from BASE]`.
func cmdWorktree(stdout io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	if len(subArgs) == 0 || subArgs[0] != "add" {
		if isHelpRequest(subArgs) {
			printFlaglessUsage(stdout, "worktree", "worktree add REPO BRANCH [--from BASE]")
			return 0, nil
		}
		return 2, errors.New("usage: stackfork worktree add REPO BRANCH [--from BASE]")
	}
	fs := newFlagSet(stdout, "worktree")
	from := fs.String("from", "", "")
	var pos []string
	args := subArgs[1:]
	for {
		if err := fs.Parse(args); err != nil {
			return parseFailure(fs, err)
		}
		if fs.NArg() == 0 {
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	if len(pos) != 2 {
		return 2, errors.New("usage: stackfork worktree add REPO BRANCH [--from BASE]")
	}
	repo, err := resolveRepoArg(pos[0], newStateBase, legacyStateBase)
	if err != nil {
		return 0, err
	}
	res, err := repos.AddWorktree(repo, pos[1], *from)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(stdout, "created %s (branch %s)\n", res.Path, res.Branch)
	for _, c := range res.Copied {
		fmt.Fprintf(stdout, "copied %s\n", c)
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(stdout, "skipped %s\n", s)
	}
	// A checkout nothing is known about gets its recipe from a prepared
	// sibling worktree now, so `start-env` has what it needs (design.md D6a).
	if _, dir, err := checkoutInstance(res.Path, res.Branch, newStateBase, legacyStateBase); err == nil {
		if _, err := prepareArgs(res.Path, repo, dir); errors.Is(err, errNoRecipe) {
			if _, _, err := seedFromSibling(stdout, res.Path, repo, collectInstances(newStateBase, legacyStateBase), err); err != nil && err != errNoRecipe {
				fmt.Fprintf(stdout, "note: %v\n", err)
			}
		}
	}
	return 0, nil
}
