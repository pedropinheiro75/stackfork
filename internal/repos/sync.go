package repos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SyncResult is what Sync found and did. Behind and Ahead are the counts
// before any fast-forward. Line is what SyncUpstream returns.
type SyncResult struct {
	Updated     bool   // the checkout was fast-forwarded
	Behind      int    // commits on the upstream the checkout lacked
	Ahead       int    // local commits not on the upstream
	Dirty       bool   // uncommitted changes to tracked files
	Line        string // one line to show, "" when there is nothing to say
	Upstream    string // e.g. origin/feature-login, "" when the branch has none
	UpstreamSHA string // the upstream commit after the fetch ("" when unknown)
	FetchFailed bool   // the fetch failed or timed out (Line holds the note)
	Blocked     bool   // behind the upstream and not safe to fast-forward (Line holds the warning)
}

// SyncUpstream is Sync's Line. It fetches the checkout's upstream and, when
// the checkout is behind it, fast-forwards only when that cannot lose or
// rewrite anything (no uncommitted changes, no local commits). Otherwise it
// touches nothing and returns a warning with the command to update. It
// returns "" when there is nothing to say (up to date, or no upstream). A
// fetch that fails or times out is a note: the start goes on with what is
// here.
func SyncUpstream(checkout string, timeout time.Duration) string {
	return Sync(checkout, timeout, true).Line
}

// Sync is SyncUpstream with a structured result. With apply false it only
// fetches and reports: it never merges, so it is a check.
func Sync(checkout string, timeout time.Duration, apply bool) SyncResult {
	return syncUpstream(checkout, timeout, apply, false)
}

// SyncUnattended is Sync(apply) for a loop nobody watches (live mode). A
// person starting an environment sees what changed; here, `merge --ff-only`
// would silently overwrite an ignored file that the incoming commits start
// tracking (it refuses only for untracked ones), so any path they add that
// already exists in the checkout blocks the fast-forward with a warning.
func SyncUnattended(checkout string, timeout time.Duration) SyncResult {
	return syncUpstream(checkout, timeout, true, true)
}

func syncUpstream(checkout string, timeout time.Duration, apply, unattended bool) SyncResult {
	var res SyncResult
	upstream, err := gitIn(context.Background(), checkout, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil || upstream == "" {
		return res
	}
	branch, _ := gitIn(context.Background(), checkout, "rev-parse", "--abbrev-ref", "HEAD")
	remote, remoteBranch, ok := strings.Cut(upstream, "/")
	if !ok {
		return res
	}
	res.Upstream = upstream
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := gitIn(ctx, checkout, "fetch", "--quiet", "--no-tags", remote, remoteBranch); err != nil {
		res.FetchFailed = true
		res.Line = fmt.Sprintf("note: could not fetch %s; starting with what is checked out", upstream)
		return res
	}
	res.UpstreamSHA, _ = gitIn(context.Background(), checkout, "rev-parse", "@{u}")
	counts, err := gitIn(context.Background(), checkout, "rev-list", "--left-right", "--count", "@{u}...HEAD")
	f := strings.Fields(counts)
	if err != nil || len(f) != 2 {
		return res
	}
	behind, _ := strconv.Atoi(f[0])
	ahead, _ := strconv.Atoi(f[1])
	res.Behind, res.Ahead = behind, ahead
	if behind == 0 {
		return res
	}
	status, _ := gitIn(context.Background(), checkout, "status", "--porcelain", "--untracked-files=no")
	dirty := status != ""
	res.Dirty = dirty
	if !dirty && ahead == 0 {
		if !apply {
			return res
		}
		if unattended {
			if clash := incomingOverwrites(checkout); len(clash) > 0 {
				res.Blocked = true
				res.Line = fmt.Sprintf("warning: %s is %s behind %s; stackfork did not update it because ignored or untracked files would be overwritten (%s). Move or remove them, then: git -C %s pull --rebase",
					branch, plural(behind, "commit"), upstream, strings.Join(clash, ", "), shellArg(checkout))
				return res
			}
		}
		if _, err := gitIn(context.Background(), checkout, "merge", "--ff-only", "--quiet", "@{u}"); err == nil {
			res.Updated = true
			res.Line = fmt.Sprintf("updated %s to %s (%s)", branch, upstream, plural(behind, "new commit"))
			return res
		}
	}
	var why, how string
	switch {
	case dirty && ahead > 0:
		why = "uncommitted changes and " + plural(ahead, "local commit")
		how = "commit or stash, then git -C " + shellArg(checkout) + " pull --rebase"
	case dirty:
		why = "uncommitted changes"
		how = "commit or stash, then git -C " + shellArg(checkout) + " pull --rebase"
	default:
		why = plural(ahead, "local commit")
		how = "git -C " + shellArg(checkout) + " pull --rebase"
	}
	res.Blocked = true
	res.Line = fmt.Sprintf("warning: %s is %s behind %s; stackfork did not update it because it has %s. Update it soon: %s",
		branch, plural(behind, "commit"), upstream, why, how)
	return res
}

// incomingOverwrites lists (at most five) paths that the commits between HEAD
// and the upstream add and that already exist in the checkout: HEAD does not
// track them, so they are untracked or ignored files. On a git failure it
// reports the failure as a clash: nothing is overwritten on a guess.
func incomingOverwrites(checkout string) []string {
	out, err := gitIn(context.Background(), checkout, "diff", "--name-only", "--diff-filter=A", "--no-renames", "-z", "HEAD", "@{u}")
	if err != nil {
		return []string{"(git diff failed)"}
	}
	var clash []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(checkout, filepath.FromSlash(p))); err == nil {
			if len(clash) == 5 {
				return append(clash, "...")
			}
			clash = append(clash, p)
		}
	}
	return clash
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// gitIn runs git in dir with no prompts and returns trimmed stdout.
func gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
