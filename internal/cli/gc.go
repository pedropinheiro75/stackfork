package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

// cmdGC is design.md D8's orphan cleanup: instances whose checkout
// directory is gone, or whose branch no longer exists in it - exactly
// inventory.StateMissingCheckout/StateMissingBranch, the same states the
// dashboard already flags with "run `stackfork gc`" warnings. Dry-run by
// default; --apply actually removes them. Never a running instance: that
// state is computed purely from checkout/branch existence (never from
// container state, see buildInstance), so a project with any currently
// running container is always excluded here regardless of what its
// checkout looks like.
func cmdGC(stdout io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
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

	inv, err := inventory.Collect(inventory.Options{
		NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, Version: Version,
	})
	if err != nil {
		return 0, err
	}
	running, err := inventory.RunningProjects()
	if err != nil {
		return 0, err
	}

	var candidates []inventory.Instance
	for _, inst := range inv.Instances {
		if inst.State != inventory.StateMissingCheckout && inst.State != inventory.StateMissingBranch {
			continue
		}
		if running[inst.Project] {
			continue
		}
		candidates = append(candidates, inst)
	}

	w := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	if len(candidates) == 0 {
		fmt.Fprintln(w, "nothing to collect")
		return 0, w.Flush()
	}
	fmt.Fprintln(w, "PROJECT\tSTATE\tROOT")
	for _, inst := range candidates {
		fmt.Fprintf(w, "%s\t%s\t%s\n", inst.Project, inst.State, inst.Root)
	}
	if !*apply {
		fmt.Fprintf(w, "\ndry run: pass --apply to remove %d instance(s) above (add --volumes --yes to also delete their data)\n", len(candidates))
		return 0, w.Flush()
	}
	if err := w.Flush(); err != nil {
		return 0, err
	}

	for _, inst := range candidates {
		stateBase := newStateBase
		if identity.IsLegacyInstance(inst.Project) {
			stateBase = legacyStateBase
		}
		instanceDir := filepath.Join(stateBase, inst.Project)

		var out bytes.Buffer
		opts := lifecycle.Options{Volumes: *volumes}
		code, err := lifecycle.Execute(&out, &out, "down", inst.Root, inst.Branch, inst.Project, instanceDir, newStateBase, legacyStateBase, opts)
		if err != nil {
			fmt.Fprintf(stdout, "%s: down failed: %s\n", inst.Project, err.Error())
			continue
		}
		if code != 0 {
			fmt.Fprintf(stdout, "%s: down exited %d: %s\n", inst.Project, code, out.String())
			continue
		}
		if err := os.RemoveAll(instanceDir); err != nil {
			fmt.Fprintf(stdout, "%s: down succeeded but state removal failed: %s\n", inst.Project, err.Error())
			continue
		}
		fmt.Fprintf(stdout, "removed %s\n", inst.Project)
	}
	return 0, nil
}
