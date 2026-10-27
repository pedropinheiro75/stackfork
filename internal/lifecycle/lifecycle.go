// Package lifecycle holds the instance actions the CLI and the dashboard
// both need to run identically (design.md D4: "each action calls the same
// internal function the CLI subcommand calls, with the same checks"). It is
// the shared home for up/down/restart and the proxy's up/down/reload, moved
// out of internal/cli so internal/dashboard can call them too without an
// import cycle (internal/cli already imports internal/dashboard to wire the
// `dashboard` subcommand).
package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
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
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// Options carries the per-action-type extras Execute needs. Zero value is
// right for a plain `down` or `restart` with no service.
type Options struct {
	Build       bool
	Profiles    []string
	Services    []string
	WaitTimeout int // up only; 0 means 180
	Volumes     bool
	Service     string   // restart/start/stop/run/exec/apply
	NoStart     bool     // apply only: recreate the service without starting it
	Targets     []string // restart/start/stop: several services at once (Service is the first)
	Command     []string // run/exec
	// Locked says the caller holds the instance lock. A stop that cascades to
	// host apps needs it to stop them, and takes it itself when this is false.
	Locked bool
	// Cascaded, when set, receives the dependents a cascading stop also stopped.
	Cascaded *[]string
	// Secrets are the values the run's preflight resolved, by manifest name,
	// with their references. Non-nil means a run: Execute then refuses any
	// effective reference the set lacks or holds with another reference. Nil
	// (a bare `run`) resolves the references itself.
	Secrets SecretValues
	// LockWant: take the instance lock around the want_running write (the
	// dashboard calls Execute without it; the CLI already holds it).
	LockWant bool
}

// ProjectNameForInstance recomputes the expected project name for root and
// branch using whichever prefix (current or legacy) the instance already
// carries, so the branch-ownership check in LoadAndValidateManifest works
// for instances resolved from either state directory (task 3.8).
func ProjectNameForInstance(root, branch, instanceProject string) (string, error) {
	if identity.IsLegacyInstance(instanceProject) {
		return identity.LegacyProjectName(root, branch)
	}
	return identity.ProjectName(root, branch)
}

// LoadAndValidateManifest reads instanceDir's manifest and snapshot and
// checks they belong to this checkout/branch and haven't drifted from the
// prepared snapshot - the one validation every lifecycle action shares.
func LoadAndValidateManifest(instanceDir, root, branch, project, action string) (*state.Manifest, error) {
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
	branchProject, err := ProjectNameForInstance(root, info.Branch, info.Project)
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

// LocalDocker refuses a non-local Docker context (task: every action needs
// this, not just up/down).
func LocalDocker() error {
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

// Execute runs one lifecycle action (up, apply, down, restart, start, stop, run or
// exec) on an
// instance: the same guards, the same docker compose invocation shape, the
// same proxy reload afterward, whoever calls it (the CLI subcommand or a
// dashboard action, design.md D4). down never receives --volumes from the
// dashboard (its action handler always passes Options{} - Volumes stays
// false); the CLI's own `down --volumes` is the only caller that ever sets
// it true.
func Execute(stdout, stderr io.Writer, action string, root, branch, project, instanceDir, newStateBase, legacyStateBase string, opts Options) (int, error) {
	info, err := LoadAndValidateManifest(instanceDir, root, branch, project, action)
	if err != nil {
		return 0, err
	}

	configPath := filepath.Join(instanceDir, "compose.json")
	if action == "up" || action == "apply" || action == "run" || action == "exec" || action == "restart" || action == "start" || action == "stop" {
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

	if err := LocalDocker(); err != nil {
		return 0, err
	}

	var secretEnv map[string]string // for the one docker compose child (design D9)
	var resolved SecretValues       // what this call resolved itself, for redacting run output
	if action == "up" || action == "apply" || action == "run" {
		// Named secrets (task 6.1, design.md D6): resolve each reference
		// into its value now, not at prepare, so a rotated env var or
		// npmrc token is picked up without re-preparing. The value goes
		// only into the environment of the docker compose child (never
		// stackfork's own) and is never written to the manifest or the
		// snapshot. Set under every
		// compose.SecretEnvAliases name (not just the secret's own manifest
		// name), so a service that references it directly as `${NPM_TOKEN}`
		// (253fb81's regression) resolves for real here, matching the same
		// literal reference Render already preserved in the snapshot.
		mapped, err := MappedSecrets(root)
		if err != nil {
			return 0, err
		}
		refs, _, unprepared := InstanceSecrets(info, mapped)
		if len(unprepared) > 0 {
			return 0, UnpreparedError(unprepared[0])
		}
		secretEnv = map[string]string{}
		resolved = SecretValues{}
		for name, ref := range refs {
			var value string
			if opts.Secrets != nil {
				// Under a run the secrets step resolved every reference, with
				// the person's flags and map applied. Nothing resolves later: a
				// prompt from here, or a value the redactor never saw, is the
				// hole this closes (review I1).
				v, ok := opts.Secrets[name]
				if !ok || v.Ref != ref {
					return 0, fmt.Errorf("secret %s was not resolved by the secrets step; start again", name)
				}
				value = v.Value
			} else if value, err = compose.ResolveSecret(name, ref); err != nil {
				return 0, err
			}
			secretEnv[name] = value
			resolved[name] = SecretValue{Ref: ref, Value: value}
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

	if action == "up" || action == "apply" {
		if err := refuseBusyRecordedPorts(info.Ports, project); err != nil {
			return 0, err
		}
	}

	if action == "down" {
		// Host processes first: they talk to the containers about to go away.
		if err := StopAllHosts(stderr, instanceDir, HostStopTimeout()); err != nil {
			fmt.Fprintln(stderr, "stackfork: some host processes could not be stopped; continuing with down")
		}
	}

	dockerCommand := []string{"compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath}
	switch action {
	case "up", "apply", "run", "exec", "start", "restart":
		// Overrides ride in a second file, so the snapshot and its hash stay
		// as prepared (add-env-overrides D2). No overrides: no file, no -f.
		derived, err := derivedOverrides(instanceDir)
		if err != nil {
			return 0, err
		}
		if derived != "" {
			dockerCommand = append(dockerCommand, "-f", derived)
		}
	}

	var cascadeNote string // stop only: the line that names what was stopped with the target
	switch action {
	case "up":
		for _, p := range opts.Profiles {
			dockerCommand = append(dockerCommand, "--profile", p)
		}
		waitTimeout := opts.WaitTimeout
		if waitTimeout == 0 {
			waitTimeout = 180
		}
		dockerCommand = append(dockerCommand, "up", "-d", "--wait", "--wait-timeout", strconv.Itoa(waitTimeout))
		if opts.Build {
			dockerCommand = append(dockerCommand, "--build")
		}
		dockerCommand = append(dockerCommand, opts.Services...)
	case "apply":
		hasService, err := serviceInSnapshot(configPath, opts.Service)
		if err != nil {
			return 0, err
		}
		if opts.Service == "" || !hasService {
			return 0, errors.New("Specify a service in this snapshot")
		}
		waitTimeout := opts.WaitTimeout
		if waitTimeout == 0 {
			waitTimeout = 180
		}
		// Only this service: its environment changed, nothing else may restart or block the wait.
		if opts.NoStart { // a stopped service: new container, old state of "stopped"
			dockerCommand = append(dockerCommand, "up", "-d", "--no-deps", "--no-start", opts.Service)
		} else {
			dockerCommand = append(dockerCommand, "up", "-d", "--no-deps", "--wait", "--wait-timeout", strconv.Itoa(waitTimeout), opts.Service)
		}
	case "down":
		dockerCommand = append(dockerCommand, "--profile", "*", "down", "--remove-orphans")
		if opts.Volumes {
			dockerCommand = append(dockerCommand, "--volumes")
		}
	case "restart", "start", "stop":
		targets := opts.Targets
		if len(targets) == 0 {
			targets = []string{opts.Service}
		}
		for _, t := range targets {
			hasService, err := serviceInSnapshot(configPath, t)
			if err != nil {
				return 0, err
			}
			if t == "" || !hasService {
				return 0, errors.New("Specify a service in this snapshot")
			}
		}
		if action == "stop" {
			// stop_cascade: what depends on the targets goes first (host apps),
			// or with them (services). Off, or nothing depends: unchanged.
			svcs, hosts := CascadeFor(root, instanceDir, targets)
			if len(svcs)+len(hosts) > 0 {
				stopped, err := stopCascadeHosts(stderr, instanceDir, hosts, opts.Locked)
				if err != nil {
					return 0, err
				}
				if dependents := append(stopped, svcs...); len(dependents) > 0 {
					sort.Strings(dependents)
					cascadeNote = cascadeLine(targets, dependents)
					if opts.Cascaded != nil {
						*opts.Cascaded = dependents
					}
				}
				targets = append(append([]string(nil), targets...), svcs...)
			}
		}
		dockerCommand = append(dockerCommand, action)
		dockerCommand = append(dockerCommand, targets...)
	case "run", "exec":
		hasService, err := serviceInSnapshot(configPath, opts.Service)
		if err != nil {
			return 0, err
		}
		if !hasService || len(opts.Command) == 0 {
			return 0, errors.New("Specify a service in this snapshot and a command")
		}
		if action == "exec" {
			dockerCommand = append(dockerCommand, "exec", "-T")
		} else {
			dockerCommand = append(dockerCommand, "run", "--rm", "--no-deps", "-T")
		}
		dockerCommand = append(dockerCommand, opts.Service)
		dockerCommand = append(dockerCommand, opts.Command...)
	}

	var upFailure bytes.Buffer // up only: what Compose said, to spot a stale network endpoint
	newCmd := func() *exec.Cmd {
		cmd := exec.Command("docker", dockerCommand...)
		cmd.Env = compose.DockerEnv(secretEnv)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if action == "up" || action == "apply" {
			upFailure.Reset()
			cmd.Stderr = io.MultiWriter(stderr, &upFailure)
		}
		return cmd
	}
	cmd := newCmd()
	if action == "run" || action == "exec" {
		// Both print what a container prints, to whoever called: redact with
		// the values known for this instance (review I2).
		// A terminal is left alone: the redactor holds bytes back, which breaks
		// interactive use, and the person at it can read the store anyway. An
		// agent runs without a terminal and stays covered.
		if known := OutputSecrets(instanceDir, info, resolved); len(known) > 0 {
			if !IsTerminal(stdout) {
				ow := redact.NewWriter(stdout, known)
				defer ow.Close()
				cmd.Stdout = ow
			}
			if !IsTerminal(stderr) {
				ew := redact.NewWriter(stderr, known)
				defer ew.Close()
				cmd.Stderr = ew
			}
		}
	}
	if action == "run" || action == "exec" || action == "up" {
		cmd.Stdin = os.Stdin
	}
	runErr := cmd.Run()
	if runErr != nil && (action == "up" || action == "apply") && RecoverStaleEndpoint(stderr, project, upFailure.String()) {
		cmd = newCmd()
		cmd.Stdin = os.Stdin
		runErr = cmd.Run() // once: a second failure is reported as it is
	}
	if action == "down" {
		CleanProjectNetworks(stderr, project)
	}

	// Task 9.2, design.md D7 layer 3: `up`, `down`, and now a per-service
	// `start`/`stop` (fix 5) are exactly the points where which containers
	// are running (and their currently published ports) can have changed,
	// so the proxy's Caddyfile is stale until reloaded from every
	// manifest. Best effort: a reload problem (or the proxy simply not
	// running) never fails the action itself.
	if runErr == nil && (action == "up" || action == "apply" || action == "down" || action == "start" || action == "stop") {
		ReloadProxyIfRunning(stderr, newStateBase, legacyStateBase)
	}

	if runErr == nil {
		if cascadeNote != "" {
			fmt.Fprintln(stdout, cascadeNote)
		}
		recordWant(action, instanceDir, project, root, opts)
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, runErr
}

func serviceInSnapshot(configPath, service string) (bool, error) {
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return false, err
	}
	parsed, err := compose.DecodeJSON(configData)
	if err != nil {
		return false, err
	}
	m, _ := parsed.(map[string]interface{})
	svc, _ := m["services"].(map[string]interface{})
	_, ok := svc[service]
	return ok, nil
}

// refuseBusyRecordedPorts checks each stable port recorded in the manifest
// (task 8.3, design.md D7 layer 2): free is fine, and a port already bound
// by this same project's own containers (e.g. left running from a previous
// `up`) is fine too, but anything else fails naming the service and port
// and suggesting `prepare --reassign-ports` - `up` never picks a different
// port silently, since redirect URIs depend on it.
func refuseBusyRecordedPorts(allocs []state.PortAlloc, project string) error {
	for _, alloc := range allocs {
		if !ports.Bound(alloc.Host) {
			continue
		}
		owned, err := portOwnedByProject(alloc.Host, project)
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("Port %d for service %s is already in use by something else; run `prepare --reassign-ports`", alloc.Host, alloc.Service)
		}
	}
	return nil
}

// portOwnedByProject reports whether every container currently bound to
// host port `port`, if any, belongs to `project`.
func portOwnedByProject(port int, project string) (bool, error) {
	out, err := exec.Command("docker", "ps", "--filter", fmt.Sprintf("publish=%d", port),
		"--format", `{{.Label "com.docker.compose.project"}}`).Output()
	if err != nil {
		return false, fmt.Errorf("Command '[docker ps --filter publish=%d ...]' returned non-zero exit status", port)
	}
	names := strings.Fields(string(out))
	if len(names) == 0 {
		return false, nil
	}
	for _, name := range names {
		if name != project {
			return false, nil
		}
	}
	return true, nil
}

// IsTerminal reports whether w is a terminal; tests replace it.
var IsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// SnapshotServices lists the services of the instance's rendered stack,
// sorted; nil when it cannot be read.
func SnapshotServices(instanceDir string) []string {
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil
	}
	parsed, err := compose.DecodeJSON(data)
	if err != nil {
		return nil
	}
	m, _ := parsed.(map[string]interface{})
	svc, _ := m["services"].(map[string]interface{})
	names := make([]string, 0, len(svc))
	for n := range svc {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ForgetInstance removes an instance's state directory (manifest, snapshot,
// runs, port records) and reloads the proxy so its route goes, the same
// removal `gc --apply` and `prune` do for each instance. Callers run it only
// after `down --volumes` succeeded.
//
// The secret references the instance recorded stay behind in
// state.KeptSecrets while its checkout exists: they are where the person said
// a secret comes from, not data, and a later Start of the same checkout and
// branch records them again instead of starting without them.
func ForgetInstance(stderr io.Writer, instanceDir, newStateBase, legacyStateBase string) error {
	var keep map[string]string
	if m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); m != nil {
		if _, err := os.Stat(m.Root); err == nil { // ponytail: an orphan's refs go with it; nothing can prepare it again
			keep = m.ResolvedSecrets()
		}
	}
	if err := os.RemoveAll(instanceDir); err != nil {
		return fmt.Errorf("state removal failed: %w", err)
	}
	if len(keep) > 0 {
		if err := os.MkdirAll(instanceDir, 0o700); err != nil {
			return fmt.Errorf("keeping the secret references failed: %w", err)
		}
		if err := state.WritePrivate(filepath.Join(instanceDir, state.KeptSecrets), keep); err != nil {
			return fmt.Errorf("keeping the secret references failed: %w", err)
		}
	}
	ReloadProxyIfRunning(stderr, newStateBase, legacyStateBase)
	return nil
}

// recordWant keeps the person's last word on the instance: a whole-stack up
// wants it running, down does not, and a stop that leaves nothing running
// does not either. up SERVICE says nothing about the rest.
func recordWant(action, instanceDir, project, root string, opts Options) {
	var want bool
	switch {
	case action == "up" && len(opts.Services) == 0:
		want = true
	case action == "down":
	case action == "stop" && !composeRunning(instanceDir, project, root):
	default:
		return
	}
	if opts.LockWant {
		if l, err := state.AcquireLock(filepath.Join(instanceDir, ".lock")); err == nil {
			defer l.Release()
		}
	}
	state.SetWantRunning(instanceDir, want)
}

// composeRunning reports whether Compose still has a running container (true
// when it cannot tell).
func composeRunning(instanceDir, project, root string) bool {
	out, err := exec.Command("docker", "compose", "-p", project, "--project-directory", root, "--env-file", os.DevNull,
		"-f", filepath.Join(instanceDir, "compose.json"), "--profile", "*", "ps", "-q", "--status", "running").Output()
	return err != nil || strings.TrimSpace(string(out)) != ""
}
