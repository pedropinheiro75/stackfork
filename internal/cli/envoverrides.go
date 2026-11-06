// envoverrides.go implements `stackfork env set|unset|overrides`
// (add-env-overrides): per-instance environment overrides of one service,
// stored outside the repository and applied by recreating only that service.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const envOverridesUsage = "env set SERVICE NAME=VALUE [--no-restart] [--allow-secret] | env unset SERVICE NAME [--no-restart] | env overrides [SERVICE] [--json] | env overrides --clear [SERVICE] [--no-restart]"

// isEnvOverrideCmd: set, unset and overrides are reserved words of `env`.
func isEnvOverrideCmd(subArgs []string) bool {
	if len(subArgs) == 0 {
		return false
	}
	switch subArgs[0] {
	case "set", "unset", "overrides":
		return true
	}
	return false
}

// splitEnvArgs separates flags from positional arguments, in any order: every
// flag of these subcommands is a boolean, and a value never starts with `-`
// because a positional is NAME=VALUE or a name.
func splitEnvArgs(args []string) (flags, pos []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			pos = append(pos, a)
		}
	}
	return flags, pos
}

func cmdEnvOverrides(stdout, stderr io.Writer, subArgs []string, root, branch, project, instanceDir, newBase, legacyBase string) (int, error) {
	sub := subArgs[0]
	fs := newFlagSet(stdout, "env "+sub)
	noRestart := fs.Bool("no-restart", false, "store the change and apply nothing")
	var allowSecret, asJSON, clear *bool
	switch sub {
	case "set":
		allowSecret = fs.Bool("allow-secret", false, "store the value of a secret-like name (people only)")
	case "overrides":
		asJSON = fs.Bool("json", false, "print JSON (values of secret-like names are masked)")
		clear = fs.Bool("clear", false, "remove the overrides of SERVICE, or of all services (people only)")
	}
	flagArgs, pos := splitEnvArgs(subArgs[1:])
	if isHelpRequest(flagArgs) {
		printFlaglessUsage(stdout, "env", envOverridesUsage)
		return 0, nil
	}
	if err := fs.Parse(flagArgs); err != nil {
		return parseFailure(fs, err)
	}
	if sub == "overrides" && !*clear && *noRestart {
		return 2, errors.New("--no-restart applies only with --clear")
	}

	apply := func(services []string) (int, error) {
		if *noRestart || len(services) == 0 {
			return 0, nil
		}
		if err := waitForIdle(instanceDir); err != nil {
			return 1, err
		}
		return lifecycleApplyEnv(stdout, root, branch, project, instanceDir, newBase, legacyBase, services)
	}

	switch sub {
	case "set":
		if len(pos) != 2 {
			return 2, errors.New("usage: stackfork " + envOverridesUsage)
		}
		name, value, ok := strings.Cut(pos[1], "=")
		if !ok {
			return 2, errors.New("env set: expected NAME=VALUE")
		}
		if err := lifecycle.SetEnvOverrides(instanceDir, pos[0], map[string]string{name: value}, nil, *allowSecret); err != nil {
			return 0, err
		}
		if m, _ := state.ReadManifest(manifestFile(instanceDir)); lifecycle.SecretLikeEnvName(m, name) {
			fmt.Fprintf(stdout, "set %s %s (stored, value hidden)\n", pos[0], name)
		} else {
			fmt.Fprintf(stdout, "set %s %s=%s\n", pos[0], name, value)
		}
		return apply([]string{pos[0]})
	case "unset":
		if len(pos) != 2 {
			return 2, errors.New("usage: stackfork " + envOverridesUsage)
		}
		if strings.Contains(pos[1], "=") {
			return 2, errors.New("env unset: expected NAME")
		}
		if err := lifecycle.SetEnvOverrides(instanceDir, pos[0], nil, []string{pos[1]}, false); err != nil {
			return 0, err
		}
		fmt.Fprintf(stdout, "unset %s %s\n", pos[0], pos[1])
		return apply([]string{pos[0]})
	}

	// overrides
	if len(pos) > 1 {
		return 2, errors.New("usage: stackfork " + envOverridesUsage)
	}
	filter := ""
	if len(pos) == 1 {
		filter = pos[0]
	}
	if *clear {
		touched, n, err := lifecycle.ClearEnvOverrides(instanceDir, filter)
		if err != nil {
			return 0, err
		}
		fmt.Fprintf(stdout, "removed %d env overrides\n", n)
		return apply(touched)
	}
	return 0, printEnvOverrides(stdout, instanceDir, filter, *asJSON)
}

// envBusyWait is how long a change waits for another run of the instance (an
// up, say) to end before it gives up and leaves the value stored (D9).
var envBusyWait = 2 * time.Minute

// waitForIdle polls the instance's run lock until it is free or envBusyWait ends.
func waitForIdle(instanceDir string) error {
	for end := time.Now().Add(envBusyWait); runs.Busy(instanceDir); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(end) {
			return errors.New("stored, not applied yet: another run is using this instance; it applies at the next `stackfork up` (or run the same `env set` again)")
		}
	}
	return nil
}

// lifecycleApplyEnv is lifecycle.ApplyEnv with the trigger of this process.
func lifecycleApplyEnv(out io.Writer, root, branch, project, instanceDir, newBase, legacyBase string, services []string) (int, error) {
	return lifecycle.ApplyEnv(out, envTrigger(), root, branch, project, instanceDir, newBase, legacyBase, services)
}

// EnvOverrideEntry and EnvOverrideService are the JSON of `env overrides --json`.
type EnvOverrideEntry struct {
	Name   string  `json:"name"`
	Value  *string `json:"value"` // null when Masked
	Masked bool    `json:"masked"`
}

type EnvOverrideService struct {
	Service   string             `json:"service"`
	Kind      string             `json:"kind"` // container, host or unknown
	Overrides []EnvOverrideEntry `json:"overrides"`
}

func printEnvOverrides(stdout io.Writer, instanceDir, filter string, asJSON bool) error {
	o, err := state.ReadEnvOverrides(instanceDir)
	if err != nil {
		return err
	}
	m, _ := state.ReadManifest(manifestFile(instanceDir))
	containers, hosts := lifecycle.EnvTargets(instanceDir, m)
	kind := map[string]string{}
	for _, n := range containers {
		kind[n] = "container"
	}
	for _, n := range hosts {
		kind[n] = "host"
	}
	names := make([]string, 0, len(o.Services))
	for svc := range o.Services {
		if filter == "" || svc == filter {
			names = append(names, svc)
		}
	}
	sort.Strings(names)
	out := []EnvOverrideService{}
	count := 0
	for _, svc := range names {
		k := kind[svc]
		if k == "" {
			k = "unknown"
		}
		entry := EnvOverrideService{Service: svc, Kind: k, Overrides: []EnvOverrideEntry{}}
		vars := o.Services[svc]
		vn := make([]string, 0, len(vars))
		for n := range vars {
			vn = append(vn, n)
		}
		sort.Strings(vn)
		for _, n := range vn {
			e := EnvOverrideEntry{Name: n}
			if lifecycle.SecretLikeEnvName(m, n) {
				e.Masked = true
			} else {
				v := vars[n]
				e.Value = &v
			}
			entry.Overrides = append(entry.Overrides, e)
			count++
		}
		out = append(out, entry)
	}
	if asJSON {
		b, err := json.MarshalIndent(struct {
			Count    int                  `json:"count"`
			Services []EnvOverrideService `json:"services"`
		}{count, out}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s\n", b)
		return nil
	}
	if count == 0 {
		fmt.Fprintln(stdout, "no env overrides")
		return nil
	}
	for _, s := range out {
		for _, e := range s.Overrides {
			v := "****"
			if e.Value != nil {
				v = *e.Value
			}
			note := ""
			if s.Kind == "unknown" {
				note = " (unknown service)"
			}
			fmt.Fprintf(stdout, "%s %s=%s%s\n", s.Service, e.Name, v, note)
		}
	}
	return nil
}

func manifestFile(instanceDir string) string { return filepath.Join(instanceDir, "manifest.json") }
