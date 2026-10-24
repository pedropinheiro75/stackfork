package lifecycle

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// StepKindEnv is the kind of the steps of a run of command "env".
const StepKindEnv = "env"

// hostRunning tells whether a recorded host app is alive; tests replace it.
var hostRunning = state.HostIdentity

// SetHostRunning swaps the liveness check of host apps and returns the old
// one; for tests.
func SetHostRunning(f func(state.HostProc) bool) func(state.HostProc) bool {
	old := hostRunning
	hostRunning = f
	return old
}

// What an apply did to one service.
const (
	EnvApplied          = "applied"             // a running container was recreated, or a running host app restarted
	EnvRecreatedStopped = "recreated (stopped)" // a stopped container was recreated and left stopped
	EnvSkipped          = "skipped"             // nothing to change now; Detail says why
)

// EnvOutcome is the result for one service of an apply.
type EnvOutcome struct {
	Service string `json:"service"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail,omitempty"`
}

// DescribeEnvOutcomes is one line for a person: "applied: web; recreated
// (stopped): db; skipped: x (no container)".
func DescribeEnvOutcomes(res []EnvOutcome) string {
	var parts []string
	for _, kind := range []string{EnvApplied, EnvRecreatedStopped, EnvSkipped} {
		var names []string
		for _, o := range res {
			if o.Kind != kind {
				continue
			}
			n := o.Service
			if o.Detail != "" {
				n += " (" + o.Detail + ")"
			}
			names = append(names, n)
		}
		if len(names) > 0 {
			parts = append(parts, kind+": "+strings.Join(names, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

// containerState reports "running", "stopped" (a container exists) or "none"
// for a Compose service. An error from Docker counts as none: nothing is
// recreated blind.
func serviceContainerState(project, service string) string {
	args := func(all bool) []string {
		a := []string{"ps"}
		if all {
			a = append(a, "-a")
		}
		return append(a, "--filter", "label=com.docker.compose.project="+project,
			"--filter", "label=com.docker.compose.service="+service, "--format", "{{.ID}}")
	}
	for _, all := range []bool{false, true} {
		out, err := exec.Command("docker", args(all)...).Output()
		if err != nil {
			return "none"
		}
		if strings.TrimSpace(string(out)) != "" {
			if all {
				return "stopped"
			}
			return "running"
		}
	}
	return "none"
}

// applySecrets are the values Execute's apply runs with: the ones the
// dashboard remembered for the same reference, else what resolves now. The
// dashboard has no shell environment, so an env: reference only works from
// the remembered value (add-env-overrides I4).
func applySecrets(instanceDir string, info *state.Manifest) (SecretValues, error) {
	mapped, err := MappedSecrets(info.Root)
	if err != nil {
		return nil, err
	}
	refs, _, unprepared := InstanceSecrets(info, mapped)
	if len(unprepared) > 0 {
		return nil, UnpreparedError(unprepared[0])
	}
	cached, _ := CachedSecrets(instanceDir)
	out := SecretValues{}
	for name, ref := range refs {
		if c, ok := cached[name]; ok && c.Ref == ref && c.Value != "" {
			out[name] = c
			continue
		}
		v, err := compose.ResolveSecret(name, ref)
		if err != nil {
			return nil, err
		}
		out[name] = SecretValue{Ref: ref, Value: v}
	}
	return out, nil
}

// ApplyEnv is ApplyEnvDetailed without the per-service outcomes.
func ApplyEnv(out io.Writer, trigger, root, branch, project, instanceDir, newBase, legacyBase string, services []string) (int, error) {
	_, code, err := ApplyEnvDetailed(out, trigger, root, branch, project, instanceDir, newBase, legacyBase, services)
	return code, err
}

// ApplyEnvDetailed applies the overrides of services as one run of command
// "env" (add-env-overrides D3) and says what it did to each. A running
// container is recreated alone (`up -d --no-deps`); a stopped one is
// recreated without starting it (`--no-start`), because start and restart
// would reuse the old container; a service with no container waits for the
// next up. A running host app is restarted; one that is not running keeps the
// override for its next start. One step per service, output redacted. A
// failed step stops the run; the overrides file stays as written. The caller
// holds no instance lock: each step takes the one it needs.
func ApplyEnvDetailed(out io.Writer, trigger, root, branch, project, instanceDir, newBase, legacyBase string, services []string) ([]EnvOutcome, int, error) {
	info, err := LoadAndValidateManifest(instanceDir, root, branch, project, "apply")
	if err != nil {
		return nil, 0, err
	}
	rec, err := runs.Begin(instanceDir, runs.Meta{Command: "env", Trigger: trigger, Project: project, Branch: branch, Out: out,
		Secrets: func() []redact.Secret { return OutputSecrets(instanceDir, info, nil) }})
	if err != nil {
		return nil, 0, err
	}
	containers, hosts := EnvTargets(instanceDir, info)
	isContainer, isHost := map[string]bool{}, map[string]bool{}
	for _, n := range containers {
		isContainer[n] = true
	}
	for _, n := range hosts {
		isHost[n] = true
	}
	type job struct {
		svc  string
		step *runs.Step
	}
	var jobs []job
	for _, s := range services {
		switch {
		case isContainer[s]:
			jobs = append(jobs, job{s, rec.Step("recreate-"+s, StepKindEnv, "recreate "+s)})
		case isHost[s]:
			jobs = append(jobs, job{s, rec.Step("restart-"+s, StepKindEnv, "restart "+s)})
		default:
			jobs = append(jobs, job{s, rec.Step("skip-"+s, StepKindEnv, "unknown service "+s)})
		}
	}
	var res []EnvOutcome
	skip := func(j job, reason, detail string) {
		fmt.Fprintf(out, "%s: %s\n", j.svc, reason)
		j.step.Skip(reason)
		res = append(res, EnvOutcome{Service: j.svc, Kind: EnvSkipped, Detail: detail})
	}
	for _, j := range jobs {
		var serr error
		code := 0
		kind := EnvApplied
		switch {
		case isContainer[j.svc]:
			st := serviceContainerState(project, j.svc)
			if st == "none" {
				skip(j, "no container; applies at next up", "no container")
				continue
			}
			noStart := st == "stopped"
			if noStart {
				kind = EnvRecreatedStopped
			}
			w := j.step.Start()
			serr = func() error {
				lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
				if err != nil {
					return err
				}
				defer lock.Release()
				vals, err := applySecrets(instanceDir, info)
				if err != nil {
					return err
				}
				if noStart {
					fmt.Fprintf(w, "recreating %s without starting it (it is stopped)\n", j.svc)
				} else {
					fmt.Fprintf(w, "recreating %s\n", j.svc)
				}
				c, err := Execute(w, w, "apply", root, branch, project, instanceDir, newBase, legacyBase, Options{Service: j.svc, NoStart: noStart, Secrets: vals})
				code = c
				if err == nil && c != 0 {
					err = fmt.Errorf("apply exited with status %d", c)
				}
				return err
			}()
			j.step.End(serr, code)
		case isHost[j.svc]:
			if proc, ok := info.HostProcs[j.svc]; !ok || !hostRunning(proc) {
				skip(j, "not running; applies at next start", "not running")
				continue
			}
			w := j.step.Start()
			fmt.Fprintf(w, "restarting %s\n", j.svc)
			serr = HostRestart(w, root, instanceDir, j.svc, newBase, legacyBase)
			j.step.End(serr, 0)
		default:
			j.step.Skip("not a service of this stack or a host app")
			res = append(res, EnvOutcome{Service: j.svc, Kind: EnvSkipped, Detail: "unknown service"})
			continue
		}
		if serr != nil {
			err := fmt.Errorf("step %s failed: %v; the override stays as written (`stackfork env unset` reverts it)", j.step.ID, serr)
			rec.Finish(err)
			return res, 1, nil
		}
		res = append(res, EnvOutcome{Service: j.svc, Kind: kind})
	}
	rec.Finish(nil)
	return res, 0, nil
}
