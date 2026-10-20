package cleanup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/repos"
)

// Detection results (design D16).
const (
	Merged = "merged" // upstream gone and the work is in the default branch
	Closed = "closed" // upstream gone and the work is not there
	Open   = "open"   // the upstream branch still exists
)

// Via names the proof that made a candidate merged.
const (
	ViaAncestry = "ancestry" // merge commit or fast-forward
	ViaCherry   = "cherry"   // every commit has an equivalent patch (rebase merge)
	ViaPatchID  = "patch-id" // the whole branch diff equals one commit (squash merge)
)

// maxSquashCommits bounds the squash-merge search (design D16). A variable
// only so a test can use a small window.
var maxSquashCommits = 500

// Result is the detection outcome for one candidate.
type Result struct {
	Branch string
	State  string // Merged, Closed or Open
	Via    string // set when Merged
	// Set by GH.Enrich when the forge answered.
	PR    int
	URL   string
	Forge string // MERGED, CLOSED or OPEN as the forge says
}

// Remote is the remote of the default branch: its configured remote, else origin.
func Remote(main string) string {
	if r, err := git(main, "config", "--get", "branch."+repos.DefaultBranchOf(main)+".remote"); err == nil && r != "" && r != "." {
		return r
	}
	return "origin"
}

// Fetch runs `git fetch --prune --no-tags <remote>` with prompts disabled.
// It is the only command here that writes (remote-tracking refs and the
// objects they need); the schedule and backoff are live.Schedule's.
func Fetch(main, remote string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", main, "fetch", "--prune", "--no-tags", "--quiet", remote)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch %s: %v: %s", remote, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Detect classifies every candidate against <remote>/<default>. It reads
// only: it runs no fetch and writes no object.
func Detect(main string, cands []Candidate) []Result {
	def := repos.DefaultBranchOf(main)
	target := Remote(main) + "/" + def
	out := make([]Result, 0, len(cands))
	for _, c := range cands {
		out = append(out, Classify(main, c.Branch, target))
	}
	return out
}

// Classify is the D16 decision for one branch against target (for example
// origin/main).
func Classify(main, branch, target string) Result {
	r := Result{Branch: branch, State: Open}
	track, err := git(main, "for-each-ref", "--format=%(upstream:track)", "refs/heads/"+branch)
	if err != nil || !strings.Contains(track, "[gone]") {
		return r
	}
	r.State = Closed
	if via := inDefault(main, branch, target); via != "" {
		r.State, r.Via = Merged, via
	}
	return r
}

// inDefault says how the branch's work is in target ("" when it is not).
func inDefault(main, branch, target string) string {
	ref := "refs/heads/" + branch
	if _, err := git(main, "merge-base", "--is-ancestor", ref, target); err == nil {
		return ViaAncestry
	}
	if cherry, err := git(main, "cherry", target, ref); err == nil && !strings.Contains("\n"+cherry, "\n+") {
		return ViaCherry
	}
	if squashMerged(main, ref, target) {
		return ViaPatchID
	}
	return ""
}

// squashMerged compares the stable patch id of the whole branch diff with the
// patch ids of the last maxSquashCommits commits of target since the merge
// base. Both are `git diff`/`git log -p` reads piped to `git patch-id`.
func squashMerged(main, ref, target string) bool {
	base, err := git(main, "merge-base", ref, target)
	if err != nil || base == "" {
		return false
	}
	mine := patchIDs(main, "diff", base, ref)
	if len(mine) != 1 {
		return false
	}
	theirs := patchIDs(main, "log", "-p", "--no-merges", "-n", fmt.Sprint(maxSquashCommits), "--format=commit %H", base+".."+target)
	for _, id := range theirs {
		if id == mine[0] {
			return true
		}
	}
	return false
}

// patchIDs runs a git command that prints patches and returns their stable
// patch ids, in order.
func patchIDs(main string, args ...string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var patch, ids bytes.Buffer
	a := exec.CommandContext(ctx, "git", append([]string{"-C", main}, args...)...)
	a.Env, a.Stdout = env, &patch
	if a.Run() != nil {
		return nil
	}
	b := exec.CommandContext(ctx, "git", "-C", main, "patch-id", "--stable")
	b.Env, b.Stdin, b.Stdout = env, &patch, &ids
	if b.Run() != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(ids.String()), "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}
