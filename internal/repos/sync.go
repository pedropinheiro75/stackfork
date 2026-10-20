package repos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SyncUpstream fetches the checkout's upstream and, when the checkout is
// behind it, fast-forwards only when that cannot lose or rewrite anything (no
// uncommitted changes, no local commits). Otherwise it touches nothing and
// returns a warning with the command to update. It returns one line to show,
// or "" when there is nothing to say (up to date, or no upstream). A fetch
// that fails or times out is a note: the start goes on with what is here.
func SyncUpstream(checkout string, timeout time.Duration) string {
	upstream, err := gitIn(context.Background(), checkout, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil || upstream == "" {
		return ""
	}
	branch, _ := gitIn(context.Background(), checkout, "rev-parse", "--abbrev-ref", "HEAD")
	remote, remoteBranch, ok := strings.Cut(upstream, "/")
	if !ok {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := gitIn(ctx, checkout, "fetch", "--quiet", "--no-tags", remote, remoteBranch); err != nil {
		return fmt.Sprintf("note: could not fetch %s; starting with what is checked out", upstream)
	}
	counts, err := gitIn(context.Background(), checkout, "rev-list", "--left-right", "--count", "@{u}...HEAD")
	f := strings.Fields(counts)
	if err != nil || len(f) != 2 {
		return ""
	}
	behind, _ := strconv.Atoi(f[0])
	ahead, _ := strconv.Atoi(f[1])
	if behind == 0 {
		return ""
	}
	status, _ := gitIn(context.Background(), checkout, "status", "--porcelain", "--untracked-files=no")
	dirty := status != ""
	if !dirty && ahead == 0 {
		if _, err := gitIn(context.Background(), checkout, "merge", "--ff-only", "--quiet", "@{u}"); err == nil {
			return fmt.Sprintf("updated %s to %s (%s)", branch, upstream, plural(behind, "new commit"))
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
	return fmt.Sprintf("warning: %s is %s behind %s; stackfork did not update it because it has %s. Update it soon: %s",
		branch, plural(behind, "commit"), upstream, why, how)
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
