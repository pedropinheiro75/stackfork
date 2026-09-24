// Package cli ports the original Python CLI's main(): argument parsing and the
// prepare/where/up/status/urls/down/run/exec dispatch, with exact parity
// (same identity, same refusals, same error text, same exit codes).
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Version is the CLI version. It is stamped at build time via
// `-ldflags "-X github.com/pedropinheiro75/stackfork/internal/cli.Version=..."`
// (see .goreleaser.yaml); a build without that flag, such as `go build` or
// `go install` from source, reports "dev".
var Version = "dev"

// Run mirrors Python's `if __name__ == '__main__': main()` wrapper,
// including the "isolated-dev: <error>" / exit 1 formatting.
func Run(args []string, stdout, stderr io.Writer) int {
	code, err := run(args, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "isolated-dev: %s\n", err.Error())
		return 1
	}
	return code
}

func run(args []string, stdout, stderr io.Writer) (int, error) {
	top := flag.NewFlagSet("stackfork", flag.ContinueOnError)
	top.SetOutput(io.Discard)
	rootFlag := top.String("root", "", "")
	npmrcFlag := top.String("npmrc", "", "")
	stateDirFlag := top.String("state-dir", "", "")
	legacyStateDirFlag := top.String("legacy-state-dir", "", "")
	var profileDirFlag stringList
	top.Var(&profileDirFlag, "profile-dir", "")
	var secretFlag stringList
	top.Var(&secretFlag, "secret", "")
	if err := top.Parse(args); err != nil {
		return 2, fmt.Errorf("%s", err.Error())
	}
	rest := top.Args()
	if len(rest) == 0 {
		return 2, errors.New("an action is required (prepare, where, up, status, urls, down, run, exec, learn, version)")
	}
	action := rest[0]
	subArgs := rest[1:]

	if action == "version" {
		fmt.Fprintf(stdout, "stackfork version %s\n", Version)
		return 0, nil
	}

	rootArg := *rootFlag
	if rootArg == "" {
		rootArg = "."
	}

	// learn is a deterministic, offline scan (task 13.2, design.md D10): it
	// works on a plain directory, Compose or not, so unlike every other
	// action it does not require a Git checkout or branch.
	if action == "learn" {
		root, err := identity.ResolvePath(rootArg)
		if err != nil {
			return 0, err
		}
		return 0, cmdLearn(stdout, root)
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

	newStateBase, err := resolveStateDir(*stateDirFlag, identity.StateDir)
	if err != nil {
		return 0, err
	}
	legacyStateBase, err := resolveStateDir(*legacyStateDirFlag, identity.LegacyStateDir)
	if err != nil {
		return 0, err
	}

	// D2: a checkout+branch that already has a legacy (idv-) instance keeps
	// operating on it - prepare, up, down, status and urls all target the
	// legacy state dir and project name, so a running stack is never
	// orphaned and a second copy is never created. A checkout+branch with
	// no legacy state gets the current (sf-) identity.
	project, err := identity.ProjectName(root, branch)
	if err != nil {
		return 0, err
	}
	stateBase := newStateBase
	legacyProject, err := identity.LegacyProjectName(root, branch)
	if err != nil {
		return 0, err
	}
	legacyManifest, err := state.ReadManifest(filepath.Join(legacyStateBase, legacyProject, "manifest.json"))
	if err != nil {
		return 0, err
	}
	if legacyManifest != nil {
		project = legacyProject
		stateBase = legacyStateBase
	}

	if action == "down" {
		instance, _, err := parseDownFlags(subArgs)
		if err != nil {
			return 2, err
		}
		if instance != "" {
			if !identity.InstancePattern.MatchString(instance) {
				return 0, errors.New("Invalid instance name")
			}
			project = instance
			// The manifest's stored project is authoritative for which
			// Compose project an instance belongs to; the prefix only
			// decides which state directory to look it up in.
			if identity.IsLegacyInstance(instance) {
				stateBase = legacyStateBase
			} else {
				stateBase = newStateBase
			}
		}
	}

	instanceDir := filepath.Join(stateBase, project)

	switch action {
	case "where":
		profileDirs, err := resolveProfileDirs(profileDirFlag)
		if err != nil {
			return 0, err
		}
		return 0, cmdWhere(stdout, root, branch, project, instanceDir, newStateBase, legacyStateBase, profileDirs)
	case "prepare":
		return 0, cmdPrepare(stdout, subArgs, root, branch, project, instanceDir, *npmrcFlag, secretFlag)
	case "up":
		return cmdUpDownRunExec(stdout, stderr, action, subArgs, root, branch, project, instanceDir)
	case "down":
		return cmdUpDownRunExec(stdout, stderr, action, subArgs, root, branch, project, instanceDir)
	case "status", "urls":
		return 0, cmdStatusURLs(stdout, action, subArgs, root, branch, project, instanceDir)
	case "run", "exec":
		return cmdUpDownRunExec(stdout, stderr, action, subArgs, root, branch, project, instanceDir)
	default:
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

// projectNameForInstance recomputes the expected project name for root and
// branch using whichever prefix (current or legacy) the instance already
// carries, so the branch-ownership check in loadAndValidateManifest works
// for instances resolved from either state directory (task 3.8).
func projectNameForInstance(root, branch, instanceProject string) (string, error) {
	if identity.IsLegacyInstance(instanceProject) {
		return identity.LegacyProjectName(root, branch)
	}
	return identity.ProjectName(root, branch)
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
	Project           string        `json:"project"`
	Root              string        `json:"root"`
	Branch            string        `json:"branch"`
	Hostname          string        `json:"hostname"`
	State             string        `json:"state"`
	PreparedInstances []preparedRef `json:"prepared_instances"`
	Profiles          []ProfileInfo `json:"profiles"`
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
		matches, err := filepath.Glob(filepath.Join(dirPrefix.base, dirPrefix.prefix+"-*", "manifest.json"))
		if err != nil {
			return err
		}
		sort.Strings(matches)
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
	return printJSON(stdout, out)
}

// --- prepare -----------------------------------------------------------

type prepareOutput struct {
	Project  string   `json:"project"`
	State    string   `json:"state"`
	Hostname string   `json:"hostname"`
	Services []string `json:"services"`
	Result   string   `json:"result"`
}

func cmdPrepare(stdout io.Writer, subArgs []string, root, branch, project, instanceDir, npmrcTop string, secretFlags []string) error {
	fs := flag.NewFlagSet("prepare", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var files stringList
	fs.Var(&files, "f", "")
	fs.Var(&files, "file", "")
	envFileFlag := fs.String("env-file", "", "")
	hostnameFlag := fs.String("hostname", "", "")
	if err := fs.Parse(subArgs); err != nil {
		return fmt.Errorf("%s", err.Error())
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
			p, err := identity.ResolvePath(f)
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
		envFile, err = identity.ResolvePath(*envFileFlag)
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

	for _, f := range resolvedFiles {
		fi, err := os.Stat(f)
		if err != nil || fi.IsDir() {
			return errors.New("No source configuration found. Create .agent-dev/nuxt.compose.yaml or select source files with -f.")
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

	rendered, err := compose.Render(root, resolvedFiles, envFile, hostname)
	if err != nil {
		return err
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

	// Named secrets (task 6.1, design.md D6): start from whatever this
	// instance already had (so a re-prepare without --secret keeps them,
	// including one folded from a legacy manifest's npmrc key, task 6.2),
	// then let --npmrc and --secret overlay by name. Only references are
	// ever computed here - never a value.
	secretRefs := map[string]string{}
	if previous != nil {
		secretRefs = previous.ResolvedSecrets()
	}
	if npmrcTop != "" {
		resolvedNpmrc, err := identity.ResolvePath(npmrcTop)
		if err != nil {
			return err
		}
		secretRefs["npm_token"] = "npmrc:" + resolvedNpmrc + "#npm.pkg.github.com"
	}
	for _, raw := range secretFlags {
		name, ref, err := compose.ParseSecretFlag(raw)
		if err != nil {
			return err
		}
		secretRefs[name] = ref
	}
	secretNames := make([]string, 0, len(secretRefs))
	for name := range secretRefs {
		secretNames = append(secretNames, name)
	}
	compose.WireSecrets(model, secretNames)

	// HTTP marking (task 7.2, design.md D7): read the io.stackfork.http/
	// .https labels a service declares, off the already-isolated model, and
	// remember them so urls doesn't have to re-parse the snapshot.
	httpPorts, httpsPorts := compose.DeclaredHTTPPorts(model)

	var envFileOut *string
	if envFile != "" {
		envFileOut = &envFile
	}

	info := &state.Manifest{
		Project: project, Root: root, Branch: branch, Hostname: hostname,
		Secrets: secretRefs, HTTPPorts: httpPorts, HTTPSPorts: httpsPorts,
		Files: resolvedFiles, EnvFile: envFileOut, Inputs: inputHashes,
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

func cmdStatusURLs(stdout io.Writer, action string, subArgs []string, root, branch, project, instanceDir string) error {
	info, err := loadAndValidateManifest(instanceDir, root, branch, project, action)
	if err != nil {
		return err
	}
	if err := localDocker(); err != nil {
		return err
	}
	configPath := filepath.Join(instanceDir, "compose.json")
	command := []string{"compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath, "--profile", "*", "ps", "--all", "--format", "json"}
	cmd := exec.Command("docker", command...)
	cmd.Env = compose.DockerEnv()
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
	if action == "urls" {
		hostname, err := identity.BrowserHostname(project, info.Hostname)
		if err != nil {
			return err
		}
		lines := compose.EndpointLines(rows, hostname, info.HTTPPorts, info.HTTPSPorts)
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
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, string(b))
	}
	return nil
}

// --- up/down/run/exec ------------------------------------------------------

func parseDownFlags(subArgs []string) (instance string, volumes bool, err error) {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&volumes, "volumes", false, "")
	fs.StringVar(&instance, "instance", "", "")
	if err := fs.Parse(subArgs); err != nil {
		return "", false, fmt.Errorf("%s", err.Error())
	}
	return instance, volumes, nil
}

func cmdUpDownRunExec(stdout, stderr io.Writer, action string, subArgs []string, root, branch, project, instanceDir string) (int, error) {
	var build bool
	var profiles, services stringList
	waitTimeout := 180
	var volumes bool
	var service string
	var command []string

	switch action {
	case "up":
		fs := flag.NewFlagSet("up", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.BoolVar(&build, "build", false, "")
		fs.Var(&profiles, "profile", "")
		fs.Var(&services, "service", "")
		var wt string
		fs.StringVar(&wt, "wait-timeout", "180", "")
		if err := fs.Parse(subArgs); err != nil {
			return 2, fmt.Errorf("%s", err.Error())
		}
		n, err := strconv.Atoi(wt)
		if err != nil {
			return 2, fmt.Errorf("invalid --wait-timeout: %s", wt)
		}
		waitTimeout = n
	case "down":
		var err error
		_, volumes, err = parseDownFlags(subArgs)
		if err != nil {
			return 2, err
		}
	case "run", "exec":
		if len(subArgs) < 1 {
			return 2, errors.New("service is required")
		}
		service = subArgs[0]
		command = subArgs[1:]
	}

	info, err := loadAndValidateManifest(instanceDir, root, branch, project, action)
	if err != nil {
		return 0, err
	}

	configPath := filepath.Join(instanceDir, "compose.json")
	if action == "up" || action == "run" || action == "exec" {
		configData, err := os.ReadFile(configPath)
		if err != nil {
			return 0, err
		}
		parsed, err := compose.DecodeJSON(configData)
		if err != nil {
			return 0, err
		}
		envFile := ""
		if info.EnvFile != nil {
			envFile = *info.EnvFile
		}
		if _, err := compose.Isolate(parsed, root, project, envFile); err != nil {
			return 0, err
		}
		for path, digest := range info.Inputs {
			fi, statErr := os.Stat(path)
			if statErr != nil || fi.IsDir() {
				return 0, fmt.Errorf("Input changed: %s; run prepare again", path)
			}
			h, err := state.SHA256File(path)
			if err != nil {
				return 0, err
			}
			if h != digest {
				return 0, fmt.Errorf("Input changed: %s; run prepare again", path)
			}
		}
	}

	if err := localDocker(); err != nil {
		return 0, err
	}

	if action == "up" || action == "run" {
		// Named secrets (task 6.1, design.md D6): resolve each reference
		// into its value now, not at prepare, so a rotated env var or
		// npmrc token is picked up without re-preparing. Only the value
		// goes into the process env passed to `docker compose`; it is
		// never written to the manifest or the snapshot.
		for name, ref := range info.ResolvedSecrets() {
			value, err := compose.ResolveSecret(name, ref)
			if err != nil {
				return 0, err
			}
			os.Setenv(name, value)
		}
		// D2: another branch stack from this checkout must be refused
		// whether it was labelled under the current namespace or the
		// legacy one, so an existing idv- stack is never orphaned and a
		// second copy of it is never started alongside it.
		for _, label := range []string{identity.LabelRoot, identity.LegacyLabelRoot} {
			active, err := exec.Command("docker", "ps", "--filter", "label="+label+"="+root,
				"--format", `{{.Label "com.docker.compose.project"}}`).Output()
			if err != nil {
				return 0, fmt.Errorf("Command '[docker ps ...]' returned non-zero exit status")
			}
			for _, name := range strings.Fields(string(active)) {
				if name != project {
					return 0, errors.New("Another branch stack still runs from this checkout. Use where, then down --instance NAME before starting this branch.")
				}
			}
		}
	}

	dockerCommand := []string{"compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath}

	switch action {
	case "up":
		for _, p := range profiles {
			dockerCommand = append(dockerCommand, "--profile", p)
		}
		dockerCommand = append(dockerCommand, "up", "-d", "--wait", "--wait-timeout", strconv.Itoa(waitTimeout))
		if build {
			dockerCommand = append(dockerCommand, "--build")
		}
		dockerCommand = append(dockerCommand, services...)
	case "down":
		dockerCommand = append(dockerCommand, "--profile", "*", "down", "--remove-orphans")
		if volumes {
			dockerCommand = append(dockerCommand, "--volumes")
		}
	case "run", "exec":
		configData, err := os.ReadFile(configPath)
		if err != nil {
			return 0, err
		}
		parsed, err := compose.DecodeJSON(configData)
		if err != nil {
			return 0, err
		}
		m, _ := parsed.(map[string]interface{})
		svc, _ := m["services"].(map[string]interface{})
		_, hasService := svc[service]
		if !hasService || len(command) == 0 {
			return 0, errors.New("Specify a service in this snapshot and a command")
		}
		if action == "exec" {
			dockerCommand = append(dockerCommand, "exec", "-T")
		} else {
			dockerCommand = append(dockerCommand, "run", "--rm", "--no-deps", "-T")
		}
		dockerCommand = append(dockerCommand, service)
		dockerCommand = append(dockerCommand, command...)
	}

	cmd := exec.Command("docker", dockerCommand...)
	cmd.Env = compose.DockerEnv()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	runErr := cmd.Run()
	if runErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, runErr
}

// --- shared validation -----------------------------------------------------

func loadAndValidateManifest(instanceDir, root, branch, project, action string) (*state.Manifest, error) {
	configPath := filepath.Join(instanceDir, "compose.json")
	manifestPath := filepath.Join(instanceDir, "manifest.json")
	if _, err := os.Stat(configPath); err != nil {
		return nil, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	if _, err := os.Stat(manifestPath); err != nil {
		return nil, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	info, err := state.ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	branchProject, err := projectNameForInstance(root, info.Branch, info.Project)
	if err != nil {
		return nil, err
	}
	if info.Root != root || info.Project != project || branchProject != project {
		return nil, errors.New("Snapshot belongs to a different checkout or branch")
	}
	if action != "down" && info.Branch != branch {
		return nil, errors.New("Snapshot belongs to a different branch")
	}
	configHash, err := state.SHA256File(configPath)
	if err != nil {
		return nil, err
	}
	if configHash != info.ConfigSHA256 {
		return nil, errors.New("Snapshot changed; edit the source Compose files and run prepare again")
	}
	return info, nil
}

func localDocker() error {
	host := os.Getenv("DOCKER_HOST")
	if host == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		out, err := exec.Command("docker", "context", "inspect").Output()
		if err != nil {
			return fmt.Errorf("Command '[docker, context, inspect]' returned non-zero exit status")
		}
		var contexts []map[string]interface{}
		if err := json.Unmarshal(out, &contexts); err != nil {
			return err
		}
		if len(contexts) == 0 {
			return errors.New("This skill operates on a local Docker daemon; select a local context first")
		}
		endpoints, _ := contexts[0]["Endpoints"].(map[string]interface{})
		docker, _ := endpoints["docker"].(map[string]interface{})
		h, _ := docker["Host"].(string)
		host = h
	}
	if !strings.HasPrefix(host, "unix://") && !strings.HasPrefix(host, "npipe://") {
		return errors.New("This skill operates on a local Docker daemon; select a local context first")
	}
	return nil
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
