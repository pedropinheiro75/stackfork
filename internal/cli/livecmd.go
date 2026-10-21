package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/live"
)

const liveUsage = "live on [REPO] | live off [REPO] | live status [REPO] [--json] | live cleanup off|suggest|auto [REPO] [--grace HOURS] [--delete-branch|--keep-branch] | live clean [REPO] BRANCH [--yes]"

// liveTargets lists a repository's instances; a test replaces it.
var liveTargets = func(newBase, legacyBase, main string) ([]live.Target, error) {
	return live.Targets(newBase, legacyBase, main, Version)
}

// cmdLive is `stackfork live on|off|status|cleanup` (add-live-mode D13, D15).
func cmdLive(stdout, stderr io.Writer, subArgs []string, rootArg, newBase, legacyBase string) (int, error) {
	if len(subArgs) == 0 || isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "live", liveUsage)
		if len(subArgs) == 0 {
			return 2, errors.New("live needs a subcommand (on, off, status, cleanup or clean)")
		}
		return 0, nil
	}
	verb, rest := subArgs[0], subArgs[1:]
	switch verb {
	case "on", "off", "status", "cleanup", "clean":
	default:
		return 2, fmt.Errorf("unknown live subcommand %q (expected on, off, status, cleanup or clean)", verb)
	}
	fs := newFlagSet(stdout, "live "+verb)
	var asJSON, delBranch, keepBranch, yes *bool
	var grace *int
	switch verb {
	case "clean":
		yes = fs.Bool("yes", false, "")
	case "status":
		asJSON = fs.Bool("json", false, "")
	case "cleanup":
		grace = fs.Int("grace", 0, "")
		delBranch = fs.Bool("delete-branch", false, "")
		keepBranch = fs.Bool("keep-branch", false, "")
	}
	pos, err := parseMixed(fs, rest)
	if errors.Is(err, flag.ErrHelp) {
		return 0, nil
	}
	if err != nil {
		return 2, err
	}
	mode := ""
	graceSet := false
	fs.Visit(func(f *flag.Flag) { graceSet = graceSet || f.Name == "grace" })
	if verb == "cleanup" {
		if len(pos) == 0 {
			return 2, errors.New("usage: stackfork live cleanup off|suggest|auto [REPO] [--grace HOURS] [--delete-branch|--keep-branch]")
		}
		mode, pos = pos[0], pos[1:]
	}
	if verb == "clean" { // live clean [REPO] BRANCH
		if len(pos) < 1 || len(pos) > 2 {
			return 2, errors.New("usage: stackfork live clean [REPO] BRANCH [--yes]")
		}
		repoArg := ""
		if len(pos) == 2 {
			repoArg = pos[0]
		}
		main, err := liveRepo(repoArg, rootArg, newBase, legacyBase)
		if err != nil {
			return 0, err
		}
		return liveClean(stdout, main, pos[len(pos)-1], *yes, newBase, legacyBase)
	}
	if len(pos) > 1 {
		return 2, fmt.Errorf("usage: stackfork %s", liveUsage)
	}
	arg := ""
	if len(pos) == 1 {
		arg = pos[0]
	}

	switch verb {
	case "on":
		main, err := liveRepo(arg, rootArg, newBase, legacyBase)
		if err != nil {
			return 0, err
		}
		return liveOn(stdout, main, newBase, legacyBase)
	case "off":
		main, err := liveRepo(arg, rootArg, newBase, legacyBase)
		if err != nil {
			return 0, err
		}
		cfg, err := config.Load()
		if err != nil {
			return 0, err
		}
		was := cfg.LiveFor(main)
		cfg.UnsetLive(main)
		if err := cfg.Save(); err != nil {
			return 0, err
		}
		if was {
			fmt.Fprintf(stdout, "live mode off for %s; a live run in progress finishes\n", main)
		} else {
			fmt.Fprintf(stdout, "live mode was already off for %s\n", main)
		}
		return 0, nil
	case "status":
		return liveStatus(stdout, arg, rootArg, *asJSON, newBase, legacyBase)
	}
	main, err := liveRepo(arg, rootArg, newBase, legacyBase)
	if err != nil {
		return 0, err
	}
	g := -1 // not given: keep the stored grace
	if graceSet {
		g = *grace
	}
	return liveCleanup(stdout, main, mode, g, *delBranch, *keepBranch)
}

// liveRepo is the main checkout of REPO (a path or a scanned name), or of the
// checkout --root is in.
func liveRepo(arg, rootArg, newBase, legacyBase string) (string, error) {
	var repo string
	var err error
	if arg != "" {
		repo, err = resolveRepoArg(arg, newBase, legacyBase)
	} else {
		var top string
		if top, err = gitToplevel(rootArg); err == nil {
			repo, err = identity.ResolvePath(top)
		}
	}
	if err != nil {
		return "", err
	}
	return mainRepo(repo), nil
}

func liveOn(stdout io.Writer, main, newBase, legacyBase string) (int, error) {
	cfg, err := config.Load()
	if err != nil {
		return 0, err
	}
	// Secrets: the dashboard has no shell environment (add-secret-vault D4).
	rows, err := lifecycle.SecretRows(main, false, nil, nil, newBase, legacyBase)
	if err != nil {
		return 0, err
	}
	var envRefs []string
	hasOP := false
	for _, r := range rows {
		switch {
		case strings.HasPrefix(r.Reference, "env:"):
			envRefs = append(envRefs, r.Name)
		case strings.HasPrefix(r.Reference, "op://"):
			hasOP = true
		}
	}
	if len(envRefs) > 0 {
		return 1, fmt.Errorf("live mode cannot resolve env: references (the dashboard does not see your shell environment): %s\n"+
			"store each value with `stackfork secret set ITEM`, then map it with `stackfork secret map %s NAME=keychain:ITEM`",
			strings.Join(envRefs, ", "), main)
	}
	// Trust: a plan whose host or prepare steps are not trusted here never runs.
	var plan knowledge.Plan
	k, err := knowledge.LoadFirst(main, main)
	if err != nil {
		return 1, fmt.Errorf("knowledge.json: %w", err)
	}
	if k != nil {
		plan = k.Plan
	}
	if ok, _ := lifecycle.HashTrusted(newBase, main, plan); !ok {
		return 1, fmt.Errorf("the plan needs trust on this machine before live mode can run it:\n%s\nreview it, then run `stackfork plan --trust`",
			strings.Join(lifecycle.ReadableDiff(newBase, main, plan), "\n"))
	}
	// Watch set and overrides, for every instance (or the checkout alone).
	ts, err := liveTargets(newBase, legacyBase, main)
	if err != nil {
		return 0, err
	}
	if len(ts) == 0 {
		ts = []live.Target{{Main: main, Root: main}}
	}
	for _, t := range ts {
		if _, _, err := live.Prepare(t); err != nil {
			return 1, fmt.Errorf("live mode cannot watch %s: %w", t.Root, err)
		}
	}

	if err := cfg.SetLive(main); err != nil {
		return 0, err
	}
	if err := cfg.Save(); err != nil {
		return 0, err
	}
	if err := live.Resume(newBase, main); err != nil {
		return 0, err
	}
	fmt.Fprintf(stdout, "live mode on for %s\n", main)
	fmt.Fprintln(stdout, "While the dashboard runs, commits on the upstream of each worktree are fetched, fast-forwarded and built on this machine.")
	fmt.Fprintln(stdout, "Branch cleanup is on suggest: it always asks first, unless you choose auto.")
	if hasOP {
		fmt.Fprintln(stdout, "1Password may ask for approval during a live run; if it is not answered within 30 seconds the run fails at secrets and live mode pauses.")
	}
	if !heldLock(live.LockPath(newBase)) {
		fmt.Fprintln(stdout, "paused: dashboard not running; start it with stackfork dashboard")
	}
	return 0, nil
}

// liveRepoJSON is one repository of `live status --json`.
type liveRepoJSON struct {
	Repo      string         `json:"repo"`
	Path      string         `json:"path"`
	Enabled   bool           `json:"enabled"`
	State     string         `json:"state"` // off, on or paused
	Reason    string         `json:"reason,omitempty"`
	Dashboard bool           `json:"dashboard_running"`
	Cleanup   config.Cleanup `json:"cleanup"`
	// Candidates are the merged and closed worktrees (branch cleanup), from the local refs.
	Candidates []live.CandidateStatus `json:"candidates"`
	Pause      *live.Pause            `json:"pause,omitempty"`
	Targets    []live.TargetStatus    `json:"targets"`
}

func liveStatus(stdout io.Writer, arg, rootArg string, asJSON bool, newBase, legacyBase string) (int, error) {
	cfg, err := config.Load()
	if err != nil {
		return 0, err
	}
	mains := cfg.LiveRepos()
	if arg != "" {
		m, err := liveRepo(arg, rootArg, newBase, legacyBase)
		if err != nil {
			return 0, err
		}
		mains = []string{m}
	}
	dash := heldLock(live.LockPath(newBase))
	out := []liveRepoJSON{}
	for _, m := range mains {
		r := liveRepoJSON{Repo: filepath.Base(m), Path: m, Enabled: cfg.LiveFor(m), State: "off", Dashboard: dash, Cleanup: cfg.CleanupFor(m), Targets: []live.TargetStatus{}, Candidates: []live.CandidateStatus{}}
		if r.Enabled {
			ts, err := liveTargets(newBase, legacyBase, m)
			if err != nil {
				return 0, err
			}
			ins := live.Inspect(newBase, m, ts)
			r.Targets, r.Pause = ins.Targets, ins.Pause
			r.Candidates = live.InspectCleanup(newBase, m, r.Cleanup, ts, time.Now()).Candidates
			r.State = "on"
			switch {
			case !dash:
				r.State, r.Reason = "paused", "dashboard not running"
			case ins.Pause != nil:
				r.State, r.Reason = "paused", ins.Pause.Reason
			}
		}
		out = append(out, r)
	}
	if asJSON {
		return 0, printJSON(stdout, out)
	}
	if len(out) == 0 {
		fmt.Fprintln(stdout, "live mode is off for every repository; turn it on with stackfork live on [REPO]")
		return 0, nil
	}
	for _, r := range out {
		fmt.Fprintf(stdout, "%s (%s): %s\n", r.Repo, r.Path, liveStateText(r))
		if r.Enabled && !r.Dashboard {
			fmt.Fprintln(stdout, "  start it with: stackfork dashboard")
			if r.Pause != nil {
				fmt.Fprintf(stdout, "  paused: %s\n", r.Pause.Reason)
			}
		}
		if r.Enabled {
			fmt.Fprintf(stdout, "  cleanup: %s\n", r.Cleanup.Mode)
			for _, c := range r.Candidates {
				fmt.Fprintf(stdout, "    %s\n", candidateText(r.Path, c))
			}
		}
		for _, t := range r.Targets {
			fmt.Fprintf(stdout, "  %s (%s, %s)\n", t.Project, t.Branch, t.State)
			if len(t.Pending) > 0 {
				fmt.Fprintf(stdout, "    pending: %s\n", pendingText(t.Pending))
			}
			if t.LastRun != nil {
				fmt.Fprintf(stdout, "    last live run: #%d %s %s\n", t.LastRun.ID, t.LastRun.Outcome, agoText(t.LastRun.At))
			}
			for _, w := range t.Warnings {
				fmt.Fprintf(stdout, "    warning: %s\n", w)
			}
		}
	}
	return 0, nil
}

// candidateText is one line of `live status` for a merged or closed worktree.
func candidateText(main string, c live.CandidateStatus) string {
	pr := ""
	if c.PR != 0 {
		pr = fmt.Sprintf(" #%d", c.PR)
	}
	switch {
	case c.Kept:
		return fmt.Sprintf("%s: %s%s, kept", c.Branch, c.State, pr)
	case !c.Eligible:
		return fmt.Sprintf("%s: %s%s, %s", c.Branch, c.State, pr, c.Warning)
	case !c.CleanAt.IsZero():
		return fmt.Sprintf("%s: %s%s, cleaned up in %s", c.Branch, c.State, pr, time.Until(c.CleanAt).Round(time.Minute))
	}
	return fmt.Sprintf("%s: %s%s, clean up with: stackfork live clean %s %s", c.Branch, c.State, pr, main, c.Branch)
}

func liveStateText(r liveRepoJSON) string {
	switch r.State {
	case "paused":
		return "on, paused: " + r.Reason
	case "on":
		return "on"
	}
	return "off"
}

// pendingText is "build 2, compose 1": the pending changes by category.
func pendingText(cs []live.Change) string {
	n := map[string]int{}
	for _, c := range cs {
		n[c.Category]++
	}
	var cats []string
	for c := range n {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	var parts []string
	for _, c := range cats {
		parts = append(parts, fmt.Sprintf("%s %d", c, n[c]))
	}
	return strings.Join(parts, ", ")
}

func liveCleanup(stdout io.Writer, main, mode string, grace int, delBranch, keepBranch bool) (int, error) {
	switch mode {
	case config.CleanupOff, config.CleanupSuggest, config.CleanupAuto:
	default:
		return 2, fmt.Errorf("unknown cleanup mode %q (off, suggest or auto)", mode)
	}
	if delBranch && keepBranch {
		return 2, errors.New("--delete-branch and --keep-branch exclude each other")
	}
	cfg, err := config.Load()
	if err != nil {
		return 0, err
	}
	cl := cfg.CleanupFor(main)
	cl.Mode = mode
	if grace >= 0 {
		cl.GraceHours = grace
	}
	if delBranch {
		cl.DeleteBranch = true
	}
	if keepBranch {
		cl.DeleteBranch = false
	}
	if err := cfg.SetCleanup(main, cl); err != nil {
		return 2, err
	}
	if err := cfg.Save(); err != nil {
		return 0, err
	}
	branch := "keep"
	if cl.DeleteBranch {
		branch = "delete"
	}
	fmt.Fprintf(stdout, "cleanup for %s: %s (grace %d h, %s the local branch)\n", main, cl.Mode, cl.GraceHours, branch)
	if cl.Mode == config.CleanupAuto {
		fmt.Fprintln(stdout, "Worktrees of merged branches and their instance databases are deleted after the grace period without asking.")
	}
	if !cfg.LiveFor(main) {
		fmt.Fprintln(stdout, "live mode is off for this repository, so nothing is checked or cleaned; turn it on with stackfork live on")
	}
	return 0, nil
}
