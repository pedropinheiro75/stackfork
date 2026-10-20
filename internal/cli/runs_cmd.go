// runs_cmd.go implements `stackfork runs [--json] [--limit N]` and
// `stackfork runs show [ID] [--json] [--step STEP]` (add-plan-runs D11): a
// read-only view of the run records of one instance. Logs are already
// redacted when they are written.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/runs"
)

var runsNow = time.Now // a variable so tests pin the clock

func cmdRuns(stdout, stderr io.Writer, subArgs []string, project, instanceDir string) (int, error) {
	if len(subArgs) > 0 && subArgs[0] == "show" {
		return cmdRunsShow(stdout, subArgs[1:], project, instanceDir)
	}
	fs := newFlagSet(stdout, "runs")
	asJSON := fs.Bool("json", false, "")
	limit := fs.Int("limit", 0, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	if fs.NArg() > 0 {
		return 2, fmt.Errorf("unexpected argument %q (use `stackfork runs show ID`)", fs.Arg(0))
	}
	list, _ := runs.List(instanceDir)
	if *limit > 0 && len(list) > *limit {
		list = list[:*limit]
	}
	if *asJSON {
		if list == nil {
			list = []*runs.Run{}
		}
		return 0, writeJSON(stdout, list)
	}
	if len(list) == 0 {
		fmt.Fprintf(stdout, "no runs yet for %s\n", project)
		return 0, nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCOMMAND\tTRIGGER\tMODE\tOUTCOME\tSTARTED\tDURATION\tFAILED STEP")
	for _, r := range list {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Command, r.Trigger, r.Mode, r.Outcome,
			agoText(r.StartedAt), durText(r.DurationMS), r.FailedStep())
	}
	return 0, tw.Flush()
}

func cmdRunsShow(stdout io.Writer, subArgs []string, project, instanceDir string) (int, error) {
	fs := newFlagSet(stdout, "runs show")
	asJSON := fs.Bool("json", false, "")
	step := fs.String("step", "", "")
	// `runs show [ID] [--json]`: a leading ID would stop flag parsing.
	args, id := subArgs, ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return parseFailure(fs, err)
	}
	if id == "" && fs.NArg() > 0 {
		id = fs.Arg(0)
	}
	var r *runs.Run
	if id == "" {
		if r = runs.Latest(instanceDir); r == nil {
			return 0, fmt.Errorf("no runs yet for %s", project)
		}
	} else {
		n, err := strconv.Atoi(id)
		if err != nil {
			return 2, fmt.Errorf("invalid run id %q", id)
		}
		if r, err = runs.Get(instanceDir, n); err != nil {
			if errors.Is(err, runs.ErrNotFound) {
				return 0, fmt.Errorf("no run %d for %s", n, project)
			}
			return 0, err
		}
	}
	if *step != "" {
		b, err := runs.Log(instanceDir, r.ID, *step)
		if errors.Is(err, runs.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("run %d has no log for step %q", r.ID, *step)
		}
		if err != nil {
			return 0, err
		}
		_, err = stdout.Write(b)
		return 0, err
	}
	if *asJSON {
		return 0, writeJSON(stdout, r)
	}
	head := fmt.Sprintf("run %d  %s from %s, %s mode  %s", r.ID, r.Command, r.Trigger, r.Mode, r.Outcome)
	if f := r.FailedStep(); f != "" {
		head += " at " + f
	}
	fmt.Fprintf(stdout, "%s  %s, %s\n", head, agoText(r.StartedAt), durText(r.DurationMS))
	if r.Error != "" {
		fmt.Fprintf(stdout, "error: %s\n", r.Error)
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	for _, s := range r.Steps {
		d, why := "", s.Reason
		if s.DurationMS > 0 || s.State == runs.Succeeded || s.State == runs.Failed {
			d = durText(s.DurationMS)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", s.ID, s.State, d, why)
	}
	if err := tw.Flush(); err != nil {
		return 0, err
	}
	if r.Retry != "" && r.Outcome != runs.Succeeded {
		fmt.Fprintf(stdout, "retry: %s\n", r.Retry)
	}
	return 0, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// agoText is a short relative time: 40s ago, 5m ago, 3h ago, 2d ago.
func agoText(t time.Time) string {
	d := runsNow().Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// durText is 0.4s, 41s or 2m05s.
func durText(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
