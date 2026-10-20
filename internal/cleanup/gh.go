package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gh limits (design D17).
const (
	ghTimeout   = 15 * time.Second
	ghAuthEvery = time.Hour
	ghCacheFor  = 6 * time.Hour
	ghMaxCalls  = 30 // per dashboard per hour
)

// ghPaths are the only places gh is looked for; PATH is never searched.
// ponytail: local copy of add-secret-vault D2's checks for `op`; reuse
// internal/secretstore's tool resolution once that package is on this branch.
var ghPaths = []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh"}

// ExecFunc runs a binary and returns its stdout. Tests replace it.
type ExecFunc func(ctx context.Context, bin string, args, env []string, dir string) ([]byte, error)

func realExec(ctx context.Context, bin string, args, env []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env, cmd.Dir = env, dir
	return cmd.Output()
}

// GH asks the forge about the pull request of a merged or closed branch. It
// can only make a result more conservative: CLOSED turns merged into closed,
// MERGED never turns closed or open into merged.
type GH struct {
	Paths       []string         // nil uses the fixed list
	RefuseUnder []string         // directories gh must not live in (scanned repository roots)
	Now         func() time.Time // nil uses time.Now
	Exec        ExecFunc         // nil runs the binary

	authAt  time.Time
	authOK  bool
	cache   map[string]ghEntry
	calls   []time.Time
	bin     string
	binDone bool
}

type ghEntry struct {
	at time.Time
	pr ghPR
}

type ghPR struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	URL    string `json:"url"`
}

func (g *GH) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// binary is the first fixed path that exists, if it passes the checks.
func (g *GH) binary() (string, error) {
	paths := g.Paths
	if paths == nil {
		paths = ghPaths
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		return checkBinary(p, g.RefuseUnder)
	}
	return "", errors.New("gh not found")
}

// env is a minimal environment: HOME, USER, LANG, a fixed PATH, and the
// GH_* and GITHUB_* variables (host and token selection).
func ghEnv() []string {
	env := []string{"HOME=" + os.Getenv("HOME"), "USER=" + os.Getenv("USER"), "LANG=" + os.Getenv("LANG"),
		"PATH=/usr/bin:/bin", "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GH_") || strings.HasPrefix(kv, "GITHUB_") {
			env = append(env, kv)
		}
	}
	return env
}

// call counts against the hourly cap and runs gh. ok is false when the cap
// is reached.
func (g *GH) call(dir string, args ...string) (out []byte, ok bool, err error) {
	now := g.now()
	kept := g.calls[:0]
	for _, t := range g.calls {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	g.calls = kept
	if len(g.calls) >= ghMaxCalls {
		return nil, false, nil
	}
	g.calls = append(g.calls, now)
	run := g.Exec
	if run == nil {
		run = realExec
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	out, err = run(ctx, g.bin, args, ghEnv(), dir)
	return out, true, err
}

// Enrich returns r with the pull request filled in and the state made more
// conservative when the forge says so. It never fails: without gh, with a
// failing auth check, at the cap or on any error it returns r unchanged.
// dir is a checkout of the repository; head is the branch's tip commit.
func (g *GH) Enrich(dir, head string, r Result) Result {
	if r.State == Open {
		return r
	}
	key := r.Branch + "@" + head
	now := g.now()
	if e, ok := g.cache[key]; ok && now.Sub(e.at) < ghCacheFor {
		return apply(r, e.pr)
	}
	if !g.binDone {
		bin, err := g.binary()
		if err != nil {
			return r // not installed, or refused: retry on a later call
		}
		g.bin, g.binDone = bin, true
	}
	if g.authAt.IsZero() || now.Sub(g.authAt) >= ghAuthEvery {
		_, ok, err := g.call(dir, "auth", "status")
		if !ok {
			return r
		}
		g.authAt, g.authOK = now, err == nil
	}
	if !g.authOK {
		return r
	}
	out, ok, err := g.call(dir, "pr", "list", "--head", r.Branch, "--state", "all", "--limit", "1", "--json", "number,state,url,mergedAt")
	if !ok || err != nil {
		return r
	}
	var prs []ghPR
	if json.Unmarshal(out, &prs) != nil {
		return r
	}
	var pr ghPR
	if len(prs) > 0 {
		pr = prs[0]
	}
	if g.cache == nil {
		g.cache = map[string]ghEntry{}
	}
	g.cache[key] = ghEntry{at: now, pr: pr}
	return apply(r, pr)
}

func apply(r Result, pr ghPR) Result {
	if pr.Number == 0 {
		return r
	}
	r.PR, r.URL, r.Forge = pr.Number, pr.URL, pr.State
	if pr.State == "CLOSED" && r.State == Merged {
		r.State, r.Via = Closed, ""
	}
	return r
}

// checkBinary resolves symbolic links and refuses a binary whose file or
// directory is writable by group or others, whose owner is neither root nor
// the current user, or that lives under one of refuseUnder.
func checkBinary(p string, refuseUnder []string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	for _, path := range []string{real, filepath.Dir(real)} {
		fi, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return "", fmt.Errorf("%s is writable by group or others", path)
		}
		if !ownerOK(fi) {
			return "", fmt.Errorf("%s has an unexpected owner", path)
		}
	}
	for _, root := range refuseUnder {
		if r, err := filepath.EvalSymlinks(root); err == nil {
			if rel, err := filepath.Rel(r, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("%s is inside %s", real, root)
			}
		}
	}
	return real, nil
}
