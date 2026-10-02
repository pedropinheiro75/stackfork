// instance.go: addressing an instance from anywhere (global --instance) and
// the errors that point a confused caller at the right instance.
package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// instanceActions are the commands that accept the global --instance.
var instanceActions = map[string]bool{
	"up": true, "down": true, "restart": true, "start": true, "stop": true,
	"status": true, "urls": true, "logs": true, "exec": true, "run": true,
	"env": true, "open": true, "host": true,
}

// servicePositional are the commands whose first positional is a service.
var servicePositional = map[string]bool{
	"restart": true, "start": true, "stop": true, "logs": true, "exec": true, "run": true,
}

func stateBaseFor(instance, newStateBase, legacyStateBase string) string {
	if identity.IsLegacyInstance(instance) {
		return legacyStateBase
	}
	return newStateBase
}

// resolveInstance reads NAME's manifest: root and branch come from it, not
// from the cwd (a missing checkout is exactly the orphan-cleanup case
// `down --instance` exists for).
func resolveInstance(instance, newStateBase, legacyStateBase string) (root, branch, instanceDir string, err error) {
	if !identity.InstancePattern.MatchString(instance) {
		return "", "", "", fmt.Errorf("Invalid instance name")
	}
	instanceDir = filepath.Join(stateBaseFor(instance, newStateBase, legacyStateBase), instance)
	info, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return "", "", "", err
	}
	if info == nil {
		return "", "", "", fmt.Errorf("no such instance %q", instance)
	}
	return info.Root, info.Branch, instanceDir, nil
}

// isInstanceName reports whether name is an existing instance.
func isInstanceName(name, newStateBase, legacyStateBase string) bool {
	if !identity.InstancePattern.MatchString(name) {
		return false
	}
	info, err := state.ReadManifest(filepath.Join(stateBaseFor(name, newStateBase, legacyStateBase), name, "manifest.json"))
	return err == nil && info != nil
}

// firstPositional is the first sub-argument that isn't a flag.
func firstPositional(subArgs []string) string {
	for _, a := range subArgs {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// instanceNameAsServiceError explains that a positional argument is an
// instance, not a service.
func instanceNameAsServiceError(action, name string) error {
	if action == "prepare" {
		return fmt.Errorf("%s is an instance name, and prepare works on the current checkout; cd to that instance's checkout (see `stackfork list`) and run prepare there", name)
	}
	return fmt.Errorf("%s is an instance name, not a service; use `stackfork %s <service> --instance %s`", name, action, name)
}

// gitCommonDir resolves the repository a checkout belongs to, shared by
// every worktree of it.
func gitCommonDir(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	dir, err := identity.ResolvePath(strings.TrimSpace(string(out)))
	if err != nil {
		return ""
	}
	return dir
}

// noStackError is the "No prepared stack" error, extended with this
// repository's instances living in other worktrees (same git common dir).
func noStackError(root, newStateBase, legacyStateBase string) error {
	msg := "No prepared stack for this checkout/branch. Run prepare first."
	common := gitCommonDir(root)
	if common == "" {
		return fmt.Errorf("%s", msg)
	}
	var lines []string
	for _, e := range []struct{ base, prefix string }{
		{newStateBase, identity.Prefix}, {legacyStateBase, identity.LegacyPrefix},
	} {
		matches, _ := state.Manifests(e.base, e.prefix)
		for _, m := range matches {
			info, err := state.ReadManifest(m)
			if err != nil || info == nil || info.Root == root {
				continue
			}
			if gitCommonDir(info.Root) == common {
				lines = append(lines, fmt.Sprintf("  %s (branch %s, %s): `cd %s`, or add `--instance %s`",
					info.Project, info.Branch, info.Root, info.Root, info.Project))
			}
		}
	}
	if len(lines) == 0 {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%s\nInstances of this repository in other worktrees:\n%s", msg, strings.Join(lines, "\n"))
}
