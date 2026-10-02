// hooks.go implements `stackfork hooks install|uninstall|status [--tool ...]`.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/hooks"
)

func cmdHooks(stdout io.Writer, subArgs []string) (int, error) {
	if isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "hooks", "hooks install|uninstall|status [--tool claude|codex|copilot]")
		return 0, nil
	}
	if len(subArgs) == 0 {
		return 2, errors.New("hooks requires a subcommand (install, uninstall, status)")
	}
	sub := subArgs[0]
	fs := newFlagSet(stdout, "hooks")
	var tools stringList
	fs.Var(&tools, "tool", "")
	if err := fs.Parse(subArgs[1:]); err != nil {
		return parseFailure(fs, err)
	}
	if sub != "install" && sub != "uninstall" && sub != "status" {
		return 2, fmt.Errorf("unknown hooks subcommand %q", sub)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return 0, err
	}
	all := hooks.Tools(home)
	var selected []hooks.Tool
	for _, t := range all {
		if len(tools) == 0 {
			selected = append(selected, t)
			continue
		}
		for _, want := range tools {
			if want == t.Name {
				selected = append(selected, t)
			}
		}
	}
	for _, want := range tools {
		if want != "claude" && want != "codex" && want != "copilot" {
			return 2, fmt.Errorf("invalid --tool %q; want claude, codex or copilot", want)
		}
	}

	bin := ""
	if exe, err := os.Executable(); err == nil && filepath.Base(exe) == "stackfork" {
		bin = exe
	}
	failed := false
	for _, t := range selected {
		switch sub {
		case "status":
			st, err := hooks.Check(t)
			switch {
			case err != nil:
				fmt.Fprintf(stdout, "%s: error: %v\n", t.Name, err)
				failed = true
			case !st.Exists:
				fmt.Fprintf(stdout, "%s: not installed (no %s)\n", t.Name, st.Path)
			case st.Installed && hooks.NeedsTrust(t):
				fmt.Fprintf(stdout, "%s: installed but untrusted (%s): %s\n", t.Name, st.Path, hooks.TrustNote)
			case st.Installed:
				fmt.Fprintf(stdout, "%s: installed (%s)\n", t.Name, st.Path)
			default:
				fmt.Fprintf(stdout, "%s: not installed (%s)\n", t.Name, st.Path)
			}
		default:
			var changed bool
			var backup string
			var err error
			if sub == "install" {
				changed, backup, err = hooks.Install(t, hooks.Command(bin, t.Name))
			} else {
				changed, backup, err = hooks.Uninstall(t)
			}
			switch {
			case err != nil:
				fmt.Fprintf(stdout, "%s: error: %v\n", t.Name, err)
				failed = true
			case !changed:
				fmt.Fprintf(stdout, "%s: already %s (%s)\n", t.Name, map[string]string{"install": "installed", "uninstall": "uninstalled"}[sub], t.Path)
			default:
				msg := fmt.Sprintf("%s: %sed %s", t.Name, sub, t.Path)
				if backup != "" {
					msg += " (backup: " + backup + ")"
				}
				fmt.Fprintln(stdout, msg)
			}
			if sub == "install" && err == nil && hooks.NeedsTrust(t) {
				fmt.Fprintf(stdout, "%s: installed - %s\n", t.Name, hooks.TrustNote)
			}
		}
	}
	if failed {
		return 1, nil
	}
	return 0, nil
}
