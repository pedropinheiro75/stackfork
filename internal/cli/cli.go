// Package cli ports the original Python CLI's main(): argument parsing and the
// prepare/where/up/status/urls/down/run/exec dispatch, with exact parity
// (same identity, same refusals, same error text, same exit codes).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/proxy"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Version is the CLI version. It is stamped at build time via
// `-ldflags "-X github.com/pedropinheiro75/stackfork/internal/cli.Version=..."`
// (see .goreleaser.yaml); a build without that flag, such as `go build` or
// `go install` from source, reports "dev".
var Version = "dev"

// Run mirrors Python's `if __name__ == '__main__': main()` wrapper,
// including the "stackfork: <error>" / exit 1 formatting.
func Run(args []string, stdout, stderr io.Writer) int {
	code, err := run(args, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "stackfork: %s\n", err.Error())
		return 1
	}
	return code
}

// registerTopFlags registers the top-level flags; argv.GlobalFlags must name
// exactly these (a test pins it).
func registerTopFlags(top *flag.FlagSet) (root, npmrc, stateDir, legacyStateDir *string, profileDir, secret *stringList, instance *string) {
	top.SetOutput(io.Discard)
	root = top.String("root", "", "")
	npmrc = top.String("npmrc", "", "")
	stateDir = top.String("state-dir", "", "")
	legacyStateDir = top.String("legacy-state-dir", "", "")
	profileDir, secret = new(stringList), new(stringList)
	top.Var(profileDir, "profile-dir", "")
	top.Var(secret, "secret", "")
	instance = top.String("instance", "", "")
	return
}

func run(args []string, stdout, stderr io.Writer) (int, error) {
	top := flag.NewFlagSet("stackfork", flag.ContinueOnError)
	rootFlag, npmrcFlag, stateDirFlag, legacyStateDirFlag, profileDirFlag, secretFlag, instanceFlag := registerTopFlags(top)

	// splitArgs recognizes a global flag wherever it appears - before or
	// after the action word - since flag.FlagSet's own Parse only ever
	// looks at what comes before the first non-flag argument (a real agent
	// session tried `stackfork prepare --npmrc X` and got "flag provided
	// but not defined: -npmrc").
	globalArgs, action, subArgs, firstUnrecognized := splitArgs(args)

	if action == "help" || action == "--help" || action == "-h" || (action == "" && len(args) == 0) {
		printOverview(stdout)
		return 0, nil
	}
	if action == "" {
		printOverview(stderr)
		if firstUnrecognized != "" {
			return 2, fmt.Errorf("unknown action %q", firstUnrecognized)
		}
		return 2, errors.New("an action is required")
	}
	if err := top.Parse(globalArgs); err != nil {
		return 2, fmt.Errorf("%s", err.Error())
	}

	if action == "version" {
		fmt.Fprintf(stdout, "stackfork version %s\n", Version)
		return 0, nil
	}

	rootArg := *rootFlag
	if rootArg == "" {
		rootArg = "."
	}

	// State dir resolution needs only flags, not a Git checkout, so proxy and
	// doctor (neither of which is scoped to one checkout+branch) can use it
	// before the git.toplevel below, same as learn.
	newStateBase, err := resolveStateDir(*stateDirFlag, identity.StateDir)
	if err != nil {
		return 0, err
	}
	legacyStateBase, err := resolveStateDir(*legacyStateDirFlag, identity.LegacyStateDir)
	if err != nil {
		return 0, err
	}

	// learn is a deterministic, offline scan (task 13.2, design.md D10): it
	// works on a plain directory, Compose or not, so unlike every other
	// action it does not require a Git checkout or branch.
	if action == "learn" {
		root, err := identity.ResolvePath(rootArg)
		if err != nil {
			return 0, err
		}
		return 0, cmdLearn(stdout, subArgs, root)
	}
	if action == "plan" {
		root, err := identity.ResolvePath(rootArg)
		if err != nil {
			return 0, err
		}
		return cmdPlan(stdout, stderr, subArgs, root, newStateBase)
	}
	// proxy and doctor operate on the tool's shared state, not one instance,
	// so they too skip the Git checkout/branch resolution below (task
	// 9.1/9.6).
	if action == "proxy" {
		return cmdProxy(stdout, stderr, subArgs, newStateBase, legacyStateBase)
	}
	if action == "doctor" {
		return cmdDoctor(stdout, subArgs, newStateBase)
	}
	// list, like proxy and doctor, reports on the whole machine's state, not
	// one checkout+branch (design.md D1/task 1.4).
	if action == "list" {
		return cmdList(stdout, subArgs, newStateBase, legacyStateBase)
	}
	if action == "repos" {
		return cmdRepos(stdout, subArgs, newStateBase, legacyStateBase)
	}
	if action == "start-env" || action == "secret" || action == "reset" {
		var global []string
		if *npmrcFlag != "" {
			global = append(global, "--npmrc", *npmrcFlag)
		}
		for _, sec := range *secretFlag {
			global = append(global, "--secret", sec)
		}
		if action == "reset" {
			checkout := rootArg
			if *instanceFlag != "" {
				r, _, _, err := resolveInstance(*instanceFlag, newStateBase, legacyStateBase)
				if err != nil {
					return 0, err
				}
				checkout = r
			}
			return cmdReset(stdout, stderr, subArgs, checkout, newStateBase, legacyStateBase, global)
		}
		if action == "secret" {
			return cmdSecret(stdout, stderr, subArgs, rootArg, newStateBase, legacyStateBase, global)
		}
		return cmdStartEnv(stdout, stderr, subArgs, rootArg, newStateBase, legacyStateBase, global)
	}
	if action == "worktree" {
		return cmdWorktree(stdout, subArgs, newStateBase, legacyStateBase)
	}
	if action == "live" {
		if *instanceFlag != "" {
			return 2, errors.New("--instance is not supported by live")
		}
		return cmdLive(stdout, stderr, subArgs, rootArg, newStateBase, legacyStateBase)
	}
	if action == "config" {
		return cmdConfig(stdout, subArgs)
	}
	if action == "dashboard" {
		return cmdDashboard(context.Background(), stdout, stderr, subArgs, newStateBase, legacyStateBase)
	}
	// gc, like list/proxy/doctor, reports on and cleans up the whole
	// machine's state, not one checkout+branch (design.md D8).
	if action == "gc" {
		return cmdGC(stdout, stderr, subArgs, newStateBase, legacyStateBase)
	}
	if action == "prune" {
		return cmdPrune(stdout, stderr, subArgs, newStateBase, legacyStateBase)
	}
	if action == "hooks" {
		return cmdHooks(stdout, subArgs)
	}
	// guard is a hook handler: it must not touch Git or Docker.
	if action == "guard" {
		return cmdGuard(os.Stdin, stdout, stderr, subArgs, newStateBase, legacyStateBase)
	}
	// A positional that is really an instance name (`restart sf-310-x`) is
	// a mistake worth naming before anything else fails confusingly.
	if (servicePositional[action] || action == "prepare") && !isHelpRequest(subArgs) {
		if name := firstPositional(subArgs); name != "" && isInstanceName(name, newStateBase, legacyStateBase) {
			return 2, instanceNameAsServiceError(action, name)
		}
	}

	// --instance NAME addresses an instance directly, from any cwd (and
	// down --instance is the orphan-cleanup escape hatch, task 3.8/D2): root
	// and branch come from the manifest, so it never requires --root to be
	// inside a Git checkout - a missing-checkout instance is exactly the
	// case it exists for.
	if *instanceFlag != "" {
		if !instanceActions[action] {
			return 2, fmt.Errorf("--instance is not supported by %s", action)
		}
		root, branch, instanceDir, err := resolveInstance(*instanceFlag, newStateBase, legacyStateBase)
		if err != nil {
			return 0, err
		}
		return dispatch(stdout, stderr, action, subArgs, root, branch, *instanceFlag, instanceDir, newStateBase, legacyStateBase, *npmrcFlag, *secretFlag, *profileDirFlag)
	}

	toplevel, err := gitToplevel(rootArg)
	if err != nil {
		return 0, err
	}
	root, err := identity.ResolvePath(toplevel)
	if err != nil {
		return 0, err
	}
	branch, err := currentBranch(root)
	if err != nil {
		return 0, err
	}

	project, instanceDir, err := checkoutInstance(root, branch, newStateBase, legacyStateBase)
	if err != nil {
		return 0, err
	}

	// A checkout with no prepared stack: say where this repository's
	// instances are instead of a bare "run prepare".
	if instanceActions[action] && action != "runs" && !isHelpRequest(subArgs) { // runs also lists a Start that failed before any manifest
		if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err != nil {
			return 0, noStackError(root, newStateBase, legacyStateBase)
		}
	}
	return dispatch(stdout, stderr, action, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase, *npmrcFlag, *secretFlag, *profileDirFlag)
}

// checkoutInstance names the instance (project and state dir) for a
// checkout+branch. D2: one that already has a legacy (idv-) instance keeps
// operating on it - prepare, up, down, status and urls all target the
// legacy state dir and project name, so a running stack is never orphaned
// and a second copy is never created. Anything else gets the current (sf-)
// identity.
func checkoutInstance(root, branch, newStateBase, legacyStateBase string) (project, instanceDir string, err error) {
	project, err = identity.ProjectName(root, branch)
	if err != nil {
		return "", "", err
	}
	stateBase := newStateBase
	legacyProject, err := identity.LegacyProjectName(root, branch)
	if err != nil {
		return "", "", err
	}
	legacyManifest, err := state.ReadManifest(filepath.Join(legacyStateBase, legacyProject, "manifest.json"))
	if err != nil {
		return "", "", err
	}
	if legacyManifest != nil {
		project = legacyProject
		stateBase = legacyStateBase
	}
	return project, filepath.Join(stateBase, project), nil
}

// dispatch runs a per-instance action once root, branch, project and
// instanceDir are known, whether from the cwd checkout or --instance.
func dispatch(stdout, stderr io.Writer, action string, subArgs []string, root, branch, project, instanceDir, newStateBase, legacyStateBase, npmrc string, secretFlag, profileDirFlag stringList) (int, error) {
	switch action {
	case "where":
		if isHelpRequest(subArgs) {
			printFlaglessUsage(stdout, action, "where")
			return 0, nil
		}
		profileDirs, err := resolveProfileDirs(profileDirFlag)
		if err != nil {
			return 0, err
		}
		return 0, cmdWhere(stdout, root, branch, project, instanceDir, newStateBase, legacyStateBase, profileDirs)
	case "prepare":
		return 0, cmdPrepare(stdout, stderr, subArgs, root, branch, project, instanceDir, npmrc, secretFlag, newStateBase, legacyStateBase)
	case "up":
		return cmdUpDownRunExec(stdout, stderr, action, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	case "down":
		return cmdUpDownRunExec(stdout, stderr, action, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	case "status", "urls":
		if isHelpRequest(subArgs) {
			printFlaglessUsage(stdout, action, action)
			return 0, nil
		}
		return 0, cmdStatusURLs(stdout, stderr, action, subArgs, root, branch, project, instanceDir, newStateBase)
	case "run", "exec":
		return cmdUpDownRunExec(stdout, stderr, action, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	case "restart", "start", "stop":
		return cmdUpDownRunExec(stdout, stderr, action, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	case "logs":
		return cmdLogs(stdout, stderr, subArgs, root, branch, project, instanceDir)
	case "runs":
		return cmdRuns(stdout, stderr, subArgs, project, instanceDir)
	case "seed":
		return cmdSeed(stdout, stderr, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	case "clone-db":
		return cmdCloneDB(stdout, subArgs, root, project, instanceDir, newStateBase, legacyStateBase)
	case "env":
		return 0, cmdEnv(stdout, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	case "host":
		return cmdHost(stdout, stderr, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	case "open":
		return 0, cmdOpen(stdout, subArgs, project, newStateBase, legacyStateBase)
	case "route":
		return 0, cmdRoute(stdout, stderr, subArgs, root, branch, project, instanceDir, newStateBase, legacyStateBase)
	default:
		printOverview(stderr)
		return 2, fmt.Errorf("unknown action %q", action)
	}
}

// resolveStateDir resolves a --state-dir/--legacy-state-dir flag value,
// falling back to defaultFn (identity.StateDir or identity.LegacyStateDir)
// when the flag is empty.
func resolveStateDir(flagValue string, defaultFn func() (string, error)) (string, error) {
	dir := flagValue
	if dir == "" {
		d, err := defaultFn()
		if err != nil {
			return "", err
		}
		dir = d
	}
	return identity.ResolvePath(dir)
}

// resolveAgainstRoot resolves a user-supplied path (prepare's -f/--env-file)
// relative to root - the checkout, already an absolute path by the time any
// caller has one to pass - instead of the process's own working directory.
// stackfork is routinely invoked with --root pointing somewhere other than
// where it's run from (an agent working from the main checkout, --root a
// worktree it prepared separately): a real session's exact failure,
// `stackfork --root .worktrees/pr630 prepare -f .agent-dev/nuxt.compose.yaml`
// run from the main checkout, needs that relative -f to mean "relative to
// .worktrees/pr630", not to the shell's own cwd. An already-absolute path
// is untouched (identity.ResolvePath still resolves its symlinks).
func resolveAgainstRoot(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return identity.ResolvePath(path)
}

// --- git helpers -----------------------------------------------------------

func gitToplevel(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("--root must be inside a Git checkout")
	}
	return strings.TrimSpace(string(out)), nil
}

func currentBranch(root string) (string, error) {
	out, _ := exec.Command("git", "-C", root, "symbolic-ref", "--short", "-q", "HEAD").Output()
	branch := strings.TrimSpace(string(out))
	if branch != "" {
		return branch, nil
	}
	rev, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("Command '[git, -C, %s, rev-parse, HEAD]' returned non-zero exit status", root)
	}
	return "detached-" + strings.TrimSpace(string(rev)), nil
}

// --- where -------------------------------------------------------------

type preparedRef struct {
	Project string `json:"project"`
	Branch  string `json:"branch"`
}

type whereOutput struct {
	Project           string                     `json:"project"`
	Root              string                     `json:"root"`
	Branch            string                     `json:"branch"`
	Hostname          string                     `json:"hostname"`
	State             string                     `json:"state"`
	PreparedInstances []preparedRef              `json:"prepared_instances"`
	Profiles          []ProfileInfo              `json:"profiles"`
	Knowledge         *inventory.KnowledgeStatus `json:"knowledge,omitempty"`
}

func cmdWhere(stdout io.Writer, root, branch, project, instanceDir, newStateBase, legacyStateBase string, profileDirs []string) error {
	manifestPath := filepath.Join(instanceDir, "manifest.json")
	saved, err := state.ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	savedHostname := ""
	if saved != nil {
		savedHostname = saved.Hostname
	}
	hostname, err := identity.BrowserHostname(project, savedHostname)
	if err != nil {
		return err
	}

	instances := []preparedRef{}
	for _, dirPrefix := range []struct {
		base, prefix string
	}{
		{newStateBase, identity.Prefix},
		{legacyStateBase, identity.LegacyPrefix},
	} {
		matches, err := state.Manifests(dirPrefix.base, dirPrefix.prefix)
		if err != nil {
			return err
		}
		for _, m := range matches {
			info, err := state.ReadManifest(m)
			if err != nil || info == nil {
				continue
			}
			if info.Root == root {
				instances = append(instances, preparedRef{Project: info.Project, Branch: info.Branch})
			}
		}
	}

	out := whereOutput{
		Project: project, Root: root, Branch: branch, Hostname: hostname,
		State: instanceDir, PreparedInstances: instances,
		Profiles: profileInfos(profileDirs),
	}
	// Task 13.7: verified knowledge goes stale here, at read time (same
	// resolution the dashboard's own inventory read already uses), never
	// rewritten to disk by a plain `where`.
	if ks, ok := inventory.ReadKnowledgeStatus(root); ok {
		out.Knowledge = ks
	}
	return printJSON(stdout, out)
}

// --- list ----------------------------------------------------------------

// cmdList prints the shared inventory (task 1.4, design.md D1): a table by
// default, the same Inventory struct as JSON with --json. The dashboard's
// GET /api/instances renders the identical struct, so the two can never
// disagree (task 2.1's golden test).
func cmdList(stdout io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	fs := newFlagSet(stdout, "list")
	jsonFlag := fs.Bool("json", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}

	inv, err := inventory.Collect(inventory.Options{
		NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, Version: Version,
	})
	if err != nil {
		return 0, err
	}
	if *jsonFlag {
		return 0, printJSON(stdout, inv)
	}

	w := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tSTATE\tBRANCH\tLEGACY\tROOT")
	for _, i := range inv.Instances {
		legacy := "-"
		if i.Legacy {
			legacy = "legacy"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", i.Project, i.State, i.Branch, legacy, i.Root)
	}
	if len(inv.Instances) == 0 {
		fmt.Fprintln(w, "(no instances)")
	}
	return 0, w.Flush()
}

// recordedPorts scans every manifest.json under both state directories
// (task 8.1: "recorded in any manifest in BOTH state dirs") and returns the
// host ports already allocated to some other instance's Ports. skipProject
// is this instance's own project name, whose prior allocations are reused
// via `existing`, not treated as a collision here. Read-only: it only opens
// and decodes manifest.json files, exactly like `where`'s existing instance
// listing (cmdWhere) does for the same two directories.
func recordedPorts(newStateBase, legacyStateBase, skipProject string) (map[int]bool, error) {
	taken := map[int]bool{}
	for _, dirPrefix := range []struct{ base, prefix string }{
		{newStateBase, identity.Prefix},
		{legacyStateBase, identity.LegacyPrefix},
	} {
		matches, err := state.Manifests(dirPrefix.base, dirPrefix.prefix)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			info, err := state.ReadManifest(m)
			if err != nil || info == nil || info.Project == skipProject {
				continue
			}
			for _, p := range info.Ports {
				taken[p.Host] = true
			}
		}
	}
	return taken, nil
}

// --- prepare -----------------------------------------------------------

type prepareOutput struct {
	Project  string   `json:"project"`
	State    string   `json:"state"`
	Hostname string   `json:"hostname"`
	Services []string `json:"services"`
	Result   string   `json:"result"`
}

func cmdPrepare(stdout, stderr io.Writer, subArgs []string, root, branch, project, instanceDir, npmrcTop string, secretFlags []string, newStateBase, legacyStateBase string) error {
	fs := newFlagSet(stdout, "prepare")
	var files stringList
	fs.Var(&files, "f", "")
	fs.Var(&files, "file", "")
	envFileFlag := fs.String("env-file", "", "")
	hostnameFlag := fs.String("hostname", "", "")
	reassignPortsFlag := fs.Bool("reassign-ports", false, "")
	randomPortsFlag := fs.Bool("random-ports", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailureErr(fs, err)
	}

	if err := os.MkdirAll(instanceDir, 0o700); err != nil {
		return err
	}
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()

	manifestPath := filepath.Join(instanceDir, "manifest.json")
	configPath := filepath.Join(instanceDir, "compose.json")
	previous, err := state.ReadManifest(manifestPath)
	if err != nil {
		return err
	}

	var resolvedFiles []string
	switch {
	case len(files) > 0:
		for _, f := range files {
			p, err := resolveAgainstRoot(root, f)
			if err != nil {
				return err
			}
			resolvedFiles = append(resolvedFiles, p)
		}
	case previous != nil:
		// ponytail: Python falls back to deriving files from `inputs` when a
		// legacy manifest lacks a "files" key; every manifest this tool has
		// ever written includes "files", so that branch is effectively dead.
		resolvedFiles = append(resolvedFiles, previous.Files...)
	default:
		resolvedFiles = []string{filepath.Join(root, ".agent-dev", "nuxt.compose.yaml")}
	}

	var envFile string
	switch {
	case *envFileFlag != "":
		envFile, err = resolveAgainstRoot(root, *envFileFlag)
		if err != nil {
			return err
		}
	case previous != nil && len(files) == 0:
		if previous.EnvFile != nil {
			envFile = *previous.EnvFile
		}
	default:
		defaultEnv := filepath.Join(root, ".agent-dev", "runtime.env")
		if fi, err := os.Stat(defaultEnv); err == nil && !fi.IsDir() {
			envFile = defaultEnv
		}
	}

	if len(files) > 0 {
		pargs := fileArgs(files)
		if *envFileFlag != "" {
			pargs = append(pargs, "--env-file", *envFileFlag)
		}
		if err := copyAdapterFromMain(stdout, root, repos.MainRepoOf(root), pargs); err != nil {
			return err
		}
	}
	for _, f := range resolvedFiles {
		fi, err := os.Stat(f)
		if err != nil || fi.IsDir() {
			return fmt.Errorf("No source configuration found at %s. Create .agent-dev/nuxt.compose.yaml or select source files with -f (relative to --root %s, not the current directory).",
				strings.Join(resolvedFiles, ", "), root)
		}
	}

	hostnameArg := *hostnameFlag
	if hostnameArg == "" && previous != nil {
		hostnameArg = previous.Hostname
	}
	hostname, err := identity.BrowserHostname(project, hostnameArg)
	if err != nil {
		return err
	}

	// Named secrets (task 6.1, design.md D6): start from whatever this
	// instance already had (so a re-prepare without --secret keeps them,
	// including one folded from a legacy manifest's npmrc key, task 6.2),
	// then let --npmrc and --secret overlay by name. Only references are
	// ever computed here - never a value. Computed before Render so it can
	// protect each name from resolving to empty (design.md D6, 253fb81's
	// regression).
	secretRefs := map[string]string{}
	if previous != nil {
		secretRefs = previous.ResolvedSecrets()
	}
	// The machine map beats the recorded references (D7), and a mapped name
	// the manifest lacks is recorded now: Render must know every name.
	mapped, err := lifecycle.MappedSecrets(root)
	if err != nil {
		return err
	}
	for name, ref := range mapped {
		secretRefs[name] = ref
	}
	if npmrcTop != "" {
		resolvedNpmrc, err := identity.ResolvePath(npmrcTop)
		if err != nil {
			return err
		}
		secretRefs["npm_token"] = "npmrc:" + resolvedNpmrc + "#npm.pkg.github.com"
	}
	for _, raw := range secretFlags {
		name, ref, err := compose.ParseCommandSecretFlag(raw)
		if err != nil {
			return err
		}
		secretRefs[name] = ref
	}
	secretNames := make([]string, 0, len(secretRefs))
	for name := range secretRefs {
		secretNames = append(secretNames, name)
	}

	rendered, unresolved, err := compose.Render(root, resolvedFiles, envFile, hostname, secretNames)
	if err != nil {
		return err
	}
	if len(unresolved) > 0 {
		fmt.Fprintf(stderr, "stackfork: warning: unresolved variable(s) in the Compose source, defaulted to empty: %s\n", strings.Join(unresolved, ", "))
	}
	model, err := compose.Isolate(rendered, root, project, envFile)
	if err != nil {
		return err
	}

	inputs := append([]string{}, resolvedFiles...)
	if envFile != "" {
		inputs = append(inputs, envFile)
	}
	inputHashes := map[string]string{}
	for _, p := range inputs {
		h, err := state.SHA256File(p)
		if err != nil {
			return err
		}
		inputHashes[p] = h
	}

	compose.WireSecrets(model, secretNames)

	// HTTP marking (task 7.2, design.md D7): read the io.stackfork.http/
	// .https labels a service declares, off the already-isolated model, and
	// remember them so urls doesn't have to re-parse the snapshot.
	httpPorts, httpsPorts := compose.DeclaredHTTPPorts(model)

	// Stable host ports (task 8.2, design.md D7 layer 2). refs is every
	// published (service, target port); refusing a scaled service that
	// publishes one is unconditional (task 8.4), whether or not stable
	// allocation itself is in play.
	refs := compose.PublishedPorts(model)
	if err := compose.RefusePublishedReplicas(model, refs); err != nil {
		return err
	}

	// D2: a legacy (idv-) instance's manifest never had allocations, and
	// re-preparing it must not start silently pinning stable ports under a
	// running stack's bookmarks/redirect URIs - only an explicit
	// --reassign-ports opts a legacy instance in. A current-prefix instance
	// gets stable ports by default; --random-ports opts back out to the
	// pre-D7 behaviour (Docker assigns a random host port every time).
	assignStable := len(refs) > 0 && !*randomPortsFlag && (!identity.IsLegacyInstance(project) || *reassignPortsFlag)

	var portAllocs []state.PortAlloc
	if assignStable {
		existing := map[compose.PortRef]int{}
		if previous != nil && !*reassignPortsFlag {
			for _, a := range previous.Ports {
				existing[compose.PortRef{Service: a.Service, Target: a.Target}] = a.Host
			}
		}
		takenRecorded, err := recordedPorts(newStateBase, legacyStateBase, project)
		if err != nil {
			return err
		}
		rng := ports.RangeFromEnv()
		hostPorts := make(map[compose.PortRef]int, len(refs))
		for _, ref := range refs {
			if host, ok := existing[ref]; ok {
				hostPorts[ref] = host
				portAllocs = append(portAllocs, state.PortAlloc{Service: ref.Service, Target: ref.Target, Host: host})
				continue
			}
			host, err := ports.Allocate(project, ref.Service, ref.Target, rng, func(p int) bool {
				return takenRecorded[p] || ports.Bound(p)
			})
			if err != nil {
				return err
			}
			takenRecorded[host] = true // don't hand this same port to a second service in this prepare
			hostPorts[ref] = host
			portAllocs = append(portAllocs, state.PortAlloc{Service: ref.Service, Target: ref.Target, Host: host})
		}
		compose.ApplyPortAllocations(model, hostPorts)
	}

	var envFileOut *string
	if envFile != "" {
		envFileOut = &envFile
	}

	info := &state.Manifest{
		Project: project, Root: root, Branch: branch, Hostname: hostname,
		Secrets: secretRefs, HTTPPorts: httpPorts, HTTPSPorts: httpsPorts,
		Ports: portAllocs, Files: resolvedFiles, EnvFile: envFileOut, Inputs: inputHashes,
	}

	if previous != nil {
		info.SeedOnce, info.SeedService, info.Seeded, info.SeedPending = previous.SeedOnce, previous.SeedService, previous.Seeded, previous.SeedPending
	}
	if err := state.WritePrivate(configPath, model); err != nil {
		return err
	}
	configHash, err := state.SHA256File(configPath)
	if err != nil {
		return err
	}
	info.ConfigSHA256 = configHash
	if err := state.WritePrivate(manifestPath, info); err != nil {
		return err
	}

	services := []string{}
	if m, ok := model.(map[string]interface{}); ok {
		if svc, ok := m["services"].(map[string]interface{}); ok {
			for name := range svc {
				services = append(services, name)
			}
		}
	}
	sort.Strings(services)

	return printJSON(stdout, prepareOutput{
		Project: project, State: instanceDir, Hostname: hostname,
		Services: services, Result: "prepared; not started",
	})
}

// --- status/urls ---------------------------------------------------------

func cmdStatusURLs(stdout, stderr io.Writer, action string, subArgs []string, root, branch, project, instanceDir, newStateBase string) error {
	info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, action)
	if err != nil {
		return err
	}
	if err := lifecycle.LocalDocker(); err != nil {
		return err
	}
	configPath := filepath.Join(instanceDir, "compose.json")
	command := []string{"compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath, "--profile", "*", "ps", "--all", "--format", "json"}
	cmd := exec.Command("docker", command...)
	cmd.Env = compose.DockerEnv(nil)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Command '[docker %s]' returned non-zero exit status", strings.Join(command, " "))
	}
	rows, err := compose.PublicStatus(out.String())
	if err != nil {
		return err
	}
	running, _ := proxy.IsRunning(proxy.ContainerName())
	if hint := proxy.DownHint(newStateBase, running); hint != "" {
		fmt.Fprintf(stderr, "stackfork: warning: %s\n", hint)
	}
	if action == "urls" {
		hostname, err := identity.BrowserHostname(project, info.Hostname)
		if err != nil {
			return err
		}
		lines := compose.EndpointLines(rows, hostname, info.HTTPPorts, info.HTTPSPorts)
		// Task 9.4, design.md D7 layer 3: once the shared proxy is running
		// and actually publishing this instance's primary HTTP service, that
		// service's line switches to the portless (or proxy-port) URL, since
		// that's the URL that actually works. Every other published line
		// (a database port, a secondary service, ...) is unaffected.
		if running {
			if configData, err := os.ReadFile(configPath); err == nil {
				if model, err := compose.DecodeJSON(configData); err == nil {
					if service, target, https, ok := compose.PrimaryHTTPService(model); ok {
						if _, found := compose.PublishedHostPort(rows, service, target); found {
							proxyPort, err := proxy.ReadPort(newStateBase)
							if err == nil {
								lines = replacePrimaryLine(lines, service, https,
									compose.PrimaryLine(service, hostname, https, proxyPort))
							}
						}
					}
				}
			}
		}
		if len(lines) == 0 {
			fmt.Fprintln(stdout, "No published endpoints; check status or start the stack.")
		} else {
			fmt.Fprintln(stdout, strings.Join(lines, "\n"))
		}
		return nil
	}
	for _, row := range rows {
		if row.Publishers == nil {
			row.Publishers = []map[string]interface{}{}
		}
		if row.Hint = compose.RestartHint(row.Service, row.State, row.Health); row.Hint != "" {
			fmt.Fprintf(stderr, "stackfork: %s\n", row.Hint)
		}
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(b))
	}
	return nil
}

// replacePrimaryLine swaps the EndpointLines entry for (service, scheme)
// with newLine (the proxied form); appends it if no matching line exists
// (a host-run primary, task 9.3, has none to replace).
func replacePrimaryLine(lines []string, service string, https bool, newLine string) []string {
	prefix := service + ": http"
	if https {
		prefix = service + ": https"
	}
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			lines[i] = newLine
			return lines
		}
	}
	return append(lines, newLine)
}

// --- up/down/run/exec ------------------------------------------------------

// parseDownFlags returns flag.ErrHelp unchanged (never collapsed to nil)
// when -h/--help was given, after printing its usage - unlike
// parseFailure/parseFailureErr's own callers, this one doesn't return
// straight back to run()'s dispatch: cmdUpDownRunExec still has real work
// to do (actually calling down) once Parse returns without error, so its
// caller needs to tell "stop, help was printed" apart from "proceed",
// which a plain nil error can't do here.
func parseDownFlags(stdout io.Writer, subArgs []string) (instance string, volumes bool, err error) {
	fs := newFlagSet(stdout, "down")
	fs.BoolVar(&volumes, "volumes", false, "")
	fs.StringVar(&instance, "instance", "", "")
	if err := fs.Parse(subArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// Parse already ran fs.Usage (newFlagSet installed it) before
			// returning this - nothing left to print, just propagate the
			// sentinel so both callers know to stop rather than proceed
			// with volumes=false/instance="" as if nothing was wrong.
			return "", false, flag.ErrHelp
		}
		return "", false, fmt.Errorf("%s (see `stackfork down --help`)", err.Error())
	}
	return instance, volumes, nil
}

// cmdUpDownRunExec parses each action's flags and delegates the actual
// guards and docker compose invocation to lifecycle.Execute (task 4.1), the
// same function the dashboard's up/down/restart actions call.
func cmdUpDownRunExec(stdout, stderr io.Writer, action string, subArgs []string, root, branch, project, instanceDir, newStateBase, legacyStateBase string) (int, error) {
	var opts lifecycle.Options
	inRun := false // up: StartEnv's own up; the enclosing run records it and seeds
	planMode := "" // up: the startup mode whose data step may seed; empty = the configured mode

	switch action {
	case "up":
		fs := newFlagSet(stdout, "up")
		fs.BoolVar(&opts.Build, "build", false, "")
		var profiles, services stringList
		fs.Var(&profiles, "profile", "")
		fs.Var(&services, "service", "")
		var wt string
		fs.StringVar(&wt, "wait-timeout", "180", "")
		fs.StringVar(&planMode, "mode", "", "")
		// --in-run is for StartEnv only, so it is not a registered flag (not in help)
		subArgs = slices.DeleteFunc(slices.Clone(subArgs), func(a string) bool {
			inRun = inRun || a == "--in-run"
			return a == "--in-run"
		})
		if inRun && !inRunHere(instanceDir) {
			return 2, errors.New("--in-run is internal: only start-env's own up may use it")
		}
		if inRun { // the enclosing Start's preflight already resolved them
			opts.Secrets = startValues(instanceDir)
		}
		if err := fs.Parse(subArgs); err != nil {
			return parseFailure(fs, err)
		}
		n, err := strconv.Atoi(wt)
		if err != nil {
			return 2, fmt.Errorf("invalid --wait-timeout: %s", wt)
		}
		if planMode != "" && planMode != knowledge.ModeContainer && planMode != knowledge.ModeHybrid {
			return 2, fmt.Errorf("invalid --mode %q (container or hybrid)", planMode)
		}
		opts.WaitTimeout = n
		opts.Profiles = profiles
		opts.Services = services
	case "down":
		var err error
		_, opts.Volumes, err = parseDownFlags(stdout, subArgs)
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		if err != nil {
			return 2, err
		}
	case "restart", "start", "stop":
		if isHelpRequest(subArgs) {
			printFlaglessUsage(stdout, action, action+" SERVICE [SERVICE...]")
			return 0, nil
		}
		if len(subArgs) < 1 {
			return 2, errors.New("service is required")
		}
		opts.Service, opts.Targets = subArgs[0], subArgs
	case "run", "exec":
		if isHelpRequest(subArgs) {
			printFlaglessUsage(stdout, action, action+" SERVICE [COMMAND...]")
			return 0, nil
		}
		if len(subArgs) < 1 {
			return 2, errors.New("service is required")
		}
		opts.Service = subArgs[0]
		opts.Command = subArgs[1:]
	}

	// An `up` of its own records a run; the run lock comes before the instance
	// lock, so a Start waiting on this up's lock is never blocked by a wait here.
	var rec *runs.Run
	var runErr error
	if action == "up" && !inRun {
		if planMode == "" {
			planMode = lifecycle.PlanMode(root, repos.MainRepoOf(root))
		}
		if _, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, action); err != nil {
			return 0, err // a refusal before the first step is not a run
		}
		var err error
		rec, err = runs.Begin(instanceDir, runs.Meta{Command: "up", Trigger: envTrigger(), Mode: planMode, Project: project, Branch: branch,
			Out: stdout, Secrets: func() []redact.Secret { return opts.Secrets.Redactions() }})
		if err != nil {
			return 0, err
		}
		rec.SetRetry("stackfork --instance " + project + " up")
		defer func() { rec.Finish(runErr) }()
		if opts.Secrets, runErr = lifecycle.InstancePreflight(rec, stdout, root, instanceDir, envTrigger() == "dashboard"); runErr != nil {
			return 0, runErr
		}
	}

	// up/down/restart/start/stop all mutate this instance's manifest/compose
	// snapshot and its proxy registration; take the same per-instance flock
	// prepare already does, so two of them racing (e.g. a script and a
	// person, or two agents) can't interleave writes. run/exec don't mutate
	// instance state the same way (a one-off container or an exec into the
	// existing one) and are left unlocked.
	switch action {
	case "up", "down", "restart", "start", "stop":
		if err := os.MkdirAll(instanceDir, 0o700); err != nil {
			return 0, err
		}
		lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
		if err != nil {
			return 0, err
		}
		defer lock.Release()
	}

	if action == "up" {
		main := repos.MainRepoOf(root)
		if planMode == "" {
			planMode = lifecycle.PlanMode(root, main)
		}
		// a failed up leaves seeded:false, so the next up seeds
		res := lifecycle.UpAndSeed(stdout, stderr, lifecycle.UpSeed{
			Run: rec, Root: root, Main: main, Branch: branch, Project: project, InstanceDir: instanceDir,
			NewBase: newStateBase, LegacyBase: legacyStateBase, Mode: planMode, Plan: lifecycle.LoadPlan(root, main), Seed: !inRun,
			Compose: func(so, se io.Writer) (int, error) {
				return lifecycle.Execute(so, se, action, root, branch, project, instanceDir, newStateBase, legacyStateBase, opts)
			},
		})
		if rec != nil {
			runErr = res.RunError(rec, project)
		}
		return res.Code, res.Err
	}
	return lifecycle.Execute(stdout, stderr, action, root, branch, project, instanceDir, newStateBase, legacyStateBase, opts)
}

// inRunDirs are the instances whose StartEnv in this process is running its
// own `up`. Only then may an `up` carry --in-run: it skips the run lock and
// the seed decision, which the enclosing run does itself.
var inRunDirs sync.Map

// markInRun marks instanceDir for the duration of StartEnv's inner up.
func markInRun(instanceDir string) (done func()) {
	k := inRunKey(instanceDir)
	inRunDirs.Store(k, true)
	return func() { inRunDirs.Delete(k) }
}

// runValues are the secret values the preflight of each Start in this
// process resolved, by instance: its inner up uses them instead of
// resolving again (design D9). Memory only.
var runValues sync.Map

func startValues(instanceDir string) lifecycle.SecretValues {
	v, _ := runValues.Load(inRunKey(instanceDir))
	vals, _ := v.(lifecycle.SecretValues)
	return vals
}

func inRunHere(instanceDir string) bool {
	_, ok := inRunDirs.Load(inRunKey(instanceDir))
	return ok
}

// inRunKey names an instance directory however the state dir was spelled.
func inRunKey(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}

// envTrigger is who started this command: the dashboard sets
// STACKFORK_TRIGGER=dashboard for the commands it runs. A label for people,
// not a security boundary.
func envTrigger() string {
	if os.Getenv("STACKFORK_TRIGGER") == "dashboard" {
		return "dashboard"
	}
	return "cli"
}

// --- output ------------------------------------------------------------

func printJSON(w io.Writer, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}
