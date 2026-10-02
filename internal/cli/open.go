// open.go implements `stackfork open [SERVICE] [--instance NAME] [--print]`:
// the instance's primary app URL (the portless one when the proxy routes
// it), opened in the browser or just printed.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

func cmdOpen(stdout io.Writer, subArgs []string, project, newStateBase, legacyStateBase string) error {
	fs := newFlagSet(stdout, "open")
	printFlag := fs.Bool("print", false, "")
	// SERVICE may come before or after --print.
	var service string
	args := subArgs
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		service, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return parseFailureErr(fs, err)
	}
	if service == "" && fs.NArg() > 0 {
		service = fs.Arg(0)
	}

	inv, err := inventory.Collect(inventory.Options{NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, Version: Version})
	if err != nil {
		return err
	}
	var inst *inventory.Instance
	for i := range inv.Instances {
		if inv.Instances[i].Project == project {
			inst = &inv.Instances[i]
		}
	}
	if inst == nil {
		return errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	if inst.State != inventory.StateRunning && inst.State != inventory.StatePartial {
		return fmt.Errorf("instance is %s, not running; run `stackfork up` first", inst.State)
	}

	url := ""
	for _, app := range inst.Apps {
		if app.URL == "" {
			continue
		}
		if service != "" {
			if app.Service == service {
				url = app.URL
				break
			}
			continue
		}
		if app.Label == "App" {
			url = app.URL
			break
		}
		if url == "" {
			url = app.URL // fall back to the first link when none is the primary app
		}
	}
	if url == "" {
		if service != "" {
			return fmt.Errorf("service %q has no HTTP URL", service)
		}
		return errors.New("this instance has no app URL; see `stackfork urls`")
	}

	fmt.Fprintln(stdout, url)
	if !*printFlag {
		openBrowser(url)
	}
	return nil
}
