package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/prune"
)

// pruneRun is one prune/gc invocation after flag parsing.
type pruneRun struct {
	scopes      prune.Scopes
	olderThan   time.Duration
	keepVolumes bool
	yes         bool // skip the prompt and delete
	jsonOut     bool
	gc          bool // gc alias: --apply == yes, gc's wording
	newBase     string
	legacyBase  string
	stdin       io.Reader
	isTTY       bool
	now         time.Time
}

func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull) // /dev/null is a character device too
	return err != nil || !os.SameFile(fi, null)
}

func cmdPrune(stdout, stderr io.Writer, subArgs []string, newBase, legacyBase string) (int, error) {
	fs := newFlagSet(stdout, "prune")
	orphaned := fs.Bool("orphaned", false, "instances whose checkout or branch is gone (never a running one)")
	stopped := fs.Bool("stopped", false, "instances with no running container")
	all := fs.Bool("all", false, "every instance, running ones too (stopped first), and the shared proxy container")
	older := fs.String("older-than", "", "only instances idle for longer than this (48h, 7d, 2w)")
	keep := fs.Bool("keep-volumes", false, "keep the instances' volumes (their data)")
	yes := fs.Bool("yes", false, "delete without the interactive confirmation")
	jsonOut := fs.Bool("json", false, "machine-readable plan/result")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	if fs.NArg() > 0 {
		return 2, fmt.Errorf("unexpected argument %q (see `stackfork prune --help`)", fs.Arg(0))
	}
	r := pruneRun{
		scopes:      prune.Scopes{Orphaned: *orphaned, Stopped: *stopped, All: *all},
		keepVolumes: *keep, yes: *yes, jsonOut: *jsonOut,
		newBase: newBase, legacyBase: legacyBase, stdin: os.Stdin, isTTY: stdinIsTTY(), now: time.Now(),
	}
	if *older != "" {
		d, err := prune.ParseDuration(*older)
		if err != nil {
			return 2, err
		}
		r.olderThan = d
	}
	if r.yes && !r.scopes.Any() {
		return 2, errors.New("--yes needs a scope: --orphaned, --stopped or --all")
	}
	return r.run(stdout, stderr)
}

// cmdGC is `prune --orphaned --keep-volumes` with gc's historical flags:
// --apply removes without a prompt, --volumes (which needs --yes) also
// removes the data.
func cmdGC(stdout, stderr io.Writer, subArgs []string, newBase, legacyBase string) (int, error) {
	fs := newFlagSet(stdout, "gc")
	apply := fs.Bool("apply", false, "")
	volumes := fs.Bool("volumes", false, "")
	yes := fs.Bool("yes", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	if *volumes && !*yes {
		return 2, errors.New("--volumes also deletes this instance's data; pass --yes to confirm")
	}
	r := pruneRun{
		scopes: prune.Scopes{Orphaned: true}, keepVolumes: !*volumes, yes: *apply, gc: true,
		newBase: newBase, legacyBase: legacyBase, now: time.Now(),
	}
	return r.run(stdout, stderr)
}

func (r pruneRun) run(stdout, stderr io.Writer) (int, error) {
	if err := lifecycle.LocalDocker(); err != nil {
		return 0, err
	}
	inv, err := inventory.Collect(inventory.Options{NewStateBase: r.newBase, LegacyStateBase: r.legacyBase, Version: Version})
	if err != nil {
		return 0, err
	}
	l, err := prune.List(!r.yes && !r.gc) // volume sizes are slow; only a dry run shows them
	if err != nil {
		return 0, err
	}
	plan := prune.BuildPlan(inv, r.newBase, r.legacyBase, l, r.now)

	scopes := r.scopes
	overview := !scopes.Any() // no scope: show every group, delete nothing
	if overview {
		scopes = prune.Scopes{All: true}
	}
	sel := prune.Select(plan.Candidates, scopes, r.olderThan, r.now)
	proxyToo := scopes.All && !overview && plan.ProxyID != ""

	execute := r.yes
	if r.jsonOut && !r.yes {
		return 0, printJSON(stdout, r.report(plan, sel, proxyToo, nil, true))
	}
	if !r.jsonOut {
		r.printPlan(stdout, plan, sel, proxyToo, overview)
	}
	if overview || (len(sel) == 0 && !proxyToo) {
		return 0, nil
	}
	if !execute && !r.gc {
		if !r.isTTY {
			fmt.Fprintln(stdout, "\ndry run: not a terminal, so nothing was deleted; pass --yes to delete, or run this interactively")
			return 1, nil
		}
		needWord := scopes.All || anyRunning(sel)
		if !confirm(r.stdin, stdout, len(sel), needWord) {
			fmt.Fprintln(stdout, "\naborted; nothing deleted")
			return 1, nil
		}
		execute = true
	}
	if !execute {
		return 0, nil
	}

	results := prune.Execute(sel, plan, prune.Options{
		KeepVolumes: r.keepVolumes, All: proxyToo, NewBase: r.newBase, LegacyBase: r.legacyBase,
	}, stderr)
	code := 0
	for _, res := range results {
		if res.Removed {
			if !r.jsonOut {
				fmt.Fprintf(stdout, "removed %s\n", res.Project)
			}
			continue
		}
		code = 1
		if !r.jsonOut {
			fmt.Fprintf(stdout, "%s: not fully removed (state kept, safe to re-run): %s\n", res.Project, strings.Join(res.Errors, "; "))
		}
	}
	if r.jsonOut {
		if err := printJSON(stdout, r.report(plan, sel, proxyToo, results, false)); err != nil {
			return 0, err
		}
	}
	return code, nil
}

func anyRunning(sel []prune.Candidate) bool {
	for _, c := range sel {
		if c.Running {
			return true
		}
	}
	return false
}

// confirm asks y/N, or for the word `prune` when running instances (or
// --all) are involved. Anything else, and EOF, declines.
func confirm(in io.Reader, out io.Writer, n int, needWord bool) bool {
	if needWord {
		fmt.Fprintf(out, "\nThis removes %d instance(s), including running ones or the shared proxy, and their data. Type `prune` to confirm: ", n)
	} else {
		fmt.Fprintf(out, "\nRemove %d instance(s) and their data? [y/N] ", n)
	}
	line, _ := bufio.NewReader(in).ReadString('\n')
	line = strings.TrimSpace(line)
	if needWord {
		return line == "prune"
	}
	return strings.EqualFold(line, "y") || strings.EqualFold(line, "yes")
}

type pruneReport struct {
	DryRun         bool              `json:"dry_run"`
	Instances      []prune.Candidate `json:"instances"`
	Orphaned       int               `json:"orphaned"`
	Stopped        int               `json:"stopped"`
	Total          int               `json:"total"`
	ReclaimBytes   int64             `json:"reclaim_bytes"`
	ReclaimPartial bool              `json:"reclaim_partial"`
	Proxy          string            `json:"proxy,omitempty"`
	Results        []prune.Result    `json:"results,omitempty"`
}

func (r pruneRun) report(plan prune.Plan, sel []prune.Candidate, proxyToo bool, results []prune.Result, dry bool) pruneReport {
	o, s, a := plan.Groups(r.now)
	b, partial := prune.Totals(sel, r.keepVolumes)
	rep := pruneReport{DryRun: dry, Instances: sel, Orphaned: o, Stopped: s, Total: a,
		ReclaimBytes: b, ReclaimPartial: partial, Results: results}
	if rep.Instances == nil {
		rep.Instances = []prune.Candidate{}
	}
	if proxyToo {
		rep.Proxy = plan.ProxyName
	}
	return rep
}

func (r pruneRun) printPlan(stdout io.Writer, plan prune.Plan, sel []prune.Candidate, proxyToo, overview bool) {
	w := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	if len(sel) == 0 && !proxyToo {
		if r.gc {
			fmt.Fprintln(w, "nothing to collect")
		} else {
			fmt.Fprintln(w, "nothing to prune")
		}
		w.Flush()
		return
	}
	if r.gc {
		fmt.Fprintln(w, "PROJECT\tSTATE\tROOT")
		for _, c := range sel {
			fmt.Fprintf(w, "%s\t%s\t%s\n", c.Project, c.State, c.Root)
		}
	} else {
		fmt.Fprintln(w, "PROJECT\tSTATE\tCHECKOUT\tBRANCH\tLEGACY\tVOLUMES\tIMAGES\tLAST ACTIVITY\tGROUPS")
		for _, c := range sel {
			state, legacy := c.State, "-"
			if c.Running {
				state += " (running)"
			}
			if c.Legacy {
				legacy = "legacy"
			}
			vols := fmt.Sprint(len(c.Volumes))
			if r.keepVolumes {
				vols = "kept"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", c.Project, state, c.Root, c.Branch, legacy,
				vols, len(c.Images), age(c.LastActivity, r.now), groupsOf(c))
		}
	}
	if proxyToo {
		fmt.Fprintf(w, "\nalso removes the shared proxy container %s\n", plan.ProxyName)
	}
	b, partial := prune.Totals(sel, r.keepVolumes)
	if !r.gc {
		est := "unknown"
		if b > 0 || !partial {
			est = prune.FormatSize(b)
			if partial {
				est = "at least " + est
			}
		}
		fmt.Fprintf(w, "\nestimated disk to reclaim: %s (best effort, shared image layers may free less)\n", est)
	}
	o, s, a := plan.Groups(r.now)
	switch {
	case overview:
		fmt.Fprintf(w, "\ndry run, nothing deleted. Choose a scope:\n")
		fmt.Fprintf(w, "  stackfork prune --orphaned   %d instance(s): checkout or branch is gone\n", o)
		fmt.Fprintf(w, "  stackfork prune --stopped    %d instance(s): not running\n", s)
		fmt.Fprintf(w, "  stackfork prune --all        %d instance(s): everything, running ones included, plus the proxy\n", a)
		fmt.Fprintln(w, "add --older-than 7d to filter by last activity, --keep-volumes to keep data, --yes to skip the prompt")
	case r.gc && !r.yes:
		fmt.Fprintf(w, "\ndry run: pass --apply to remove %d instance(s) above (add --volumes --yes to also delete their data)\n", len(sel))
	}
	w.Flush()
}

func groupsOf(c prune.Candidate) string {
	var g []string
	if c.Orphaned && !c.Running {
		g = append(g, "orphaned")
	}
	if !c.Running {
		g = append(g, "stopped")
	}
	g = append(g, "all")
	return strings.Join(g, ",")
}

func age(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
