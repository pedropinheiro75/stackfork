package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// StartOptions is everything StartEnv needs; the dashboard fills the same
// struct the CLI does, so both run one code path (design.md D6).
type StartOptions struct {
	Checkout        string
	NewStateBase    string
	LegacyStateBase string
	Global          []string // extra global flags (--npmrc, --secret) to forward
	ProfileDirs     []string
}

// StartEnv prepares (only when needed) and starts the instance of one
// checkout, writing progress lines to out. It refuses a project stackfork
// does not know how to run (design.md D3/D6).
func StartEnv(out, errOut io.Writer, o StartOptions) error {
	toplevel, err := gitToplevel(o.Checkout)
	if err != nil {
		return fmt.Errorf("%s is not a git checkout", o.Checkout)
	}
	root, err := identity.ResolvePath(toplevel)
	if err != nil {
		return err
	}
	branch, err := currentBranch(root)
	if err != nil {
		return err
	}
	main := repos.MainRepoOf(root)
	if main == "" {
		main = root
	}
	var insts []inventory.Instance
	if inv, err := inventory.Collect(inventory.Options{NewStateBase: o.NewStateBase, LegacyStateBase: o.LegacyStateBase, Version: Version}); err == nil {
		insts = inv.Instances
	}
	level, reason := repos.ReadinessOf(filepath.Base(main), []string{main, root}, insts, o.ProfileDirs)
	if level != repos.Ready {
		return fmt.Errorf("%s is %s (%s); stackfork only starts projects it already knows how to run. Ask your agent to learn it: `stackfork repos --prompt %s %s`",
			filepath.Base(main), level, reason, filepath.Base(main), branch)
	}
	project, instanceDir, err := checkoutInstance(root, branch, o.NewStateBase, o.LegacyStateBase)
	if err != nil {
		return err
	}
	base := append([]string{"--state-dir", o.NewStateBase, "--legacy-state-dir", o.LegacyStateBase, "--root", root}, o.Global...)

	if needsPrepare(instanceDir, root, branch, project) {
		args, err := prepareArgs(root, main, instanceDir)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "preparing %s (%s)\n", project, branch)
		var pOut, pErr bytes.Buffer
		code, err := run(append(append(append([]string{}, base...), "prepare"), args...), &pOut, &pErr)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("prepare exited with status %d", code)
		}
		if w := strings.TrimSpace(pErr.String()); w != "" {
			fmt.Fprintln(out, w)
		}
	} else {
		fmt.Fprintf(out, "%s is already prepared\n", project)
	}

	fmt.Fprintf(out, "starting %s\n", project)
	code, err := run(append(append([]string{}, base...), "up"), out, errOut)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("up exited with status %d", code)
	}
	fmt.Fprintf(out, "started %s; see `stackfork --root %s urls`\n", project, root)
	return nil
}

// needsPrepare is true when the checkout has no usable prepared instance:
// no manifest, a snapshot that no longer matches, or an input file that
// changed since prepare.
func needsPrepare(instanceDir, root, branch, project string) bool {
	info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "up")
	if err != nil {
		return true
	}
	for path, digest := range info.Inputs {
		if h, err := state.SHA256File(path); err != nil || h != digest {
			return true
		}
	}
	return false
}

// prepareArgs decides what to pass to `prepare`: nothing when an instance
// manifest already records its files, else the recorded recipe's
// -f/--env-file, else the project's adapter compose files.
func prepareArgs(root, main, instanceDir string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err == nil {
		return nil, nil // prepare reuses the files, env file and secrets it recorded
	}
	var k *knowledge.Knowledge
	for _, dir := range []string{root, main} {
		if kk, err := knowledge.Load(filepath.Join(dir, ".stackfork", "knowledge.json")); err == nil {
			k = kk
			break
		}
	}
	if k != nil {
		if args := recipePrepareArgs(k.Recipe.Prepare); len(args) > 0 {
			return args, nil
		}
		var files []string
		for _, f := range k.AdapterFiles {
			if strings.HasSuffix(f, ".compose.yaml") || strings.HasSuffix(f, ".compose.yml") {
				files = append(files, f)
			}
		}
		if len(files) > 0 {
			return fileArgs(files), nil
		}
	}
	for _, dir := range []string{".stackfork", ".agent-dev"} {
		if m, _ := filepath.Glob(filepath.Join(root, dir, "*.compose.yaml")); len(m) > 0 {
			sort.Strings(m)
			rel, _ := filepath.Rel(root, m[0])
			return fileArgs([]string{rel}), nil
		}
	}
	return nil, errors.New("no recorded prepare recipe or adapter compose file for this project; run `stackfork prepare -f FILE` once")
}

func fileArgs(files []string) []string {
	var a []string
	for _, f := range files {
		a = append(a, "-f", f)
	}
	return a
}

// recipePrepareArgs pulls the safe prepare flags out of a recorded recipe
// command ("stackfork prepare -f x --env-file y"); anything unexpected, or a
// shell variable, makes it give up and return nil.
func recipePrepareArgs(cmd string) []string {
	toks := strings.Fields(cmd)
	for i, t := range toks {
		if t != "prepare" {
			continue
		}
		var out []string
		rest := toks[i+1:]
		for j := 0; j < len(rest); j++ {
			switch f := rest[j]; f {
			case "-f", "--file", "--env-file", "--hostname":
				if j+1 >= len(rest) || strings.ContainsAny(rest[j+1], "$`;&|<>") {
					return nil
				}
				j++
				out = append(out, f, strings.Trim(rest[j], `"'`))
			case "--reassign-ports", "--random-ports":
				out = append(out, f)
			default:
				return nil
			}
		}
		return out
	}
	return nil
}

// cmdStartEnv is `stackfork start-env [CHECKOUT]`.
func cmdStartEnv(stdout, stderr io.Writer, subArgs []string, rootArg, newStateBase, legacyStateBase string, global []string) (int, error) {
	if isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "start-env", "start-env [CHECKOUT]")
		return 0, nil
	}
	if len(subArgs) > 1 {
		return 2, errors.New("usage: stackfork start-env [CHECKOUT]")
	}
	checkout := rootArg
	if len(subArgs) == 1 {
		checkout = subArgs[0]
	}
	profiles, err := resolveProfileDirs(nil)
	if err != nil {
		return 0, err
	}
	return 0, StartEnv(stdout, stderr, StartOptions{
		Checkout: checkout, NewStateBase: newStateBase, LegacyStateBase: legacyStateBase,
		Global: global, ProfileDirs: profiles,
	})
}
