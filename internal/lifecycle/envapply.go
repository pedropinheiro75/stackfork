package lifecycle

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

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

// serviceRunning reports whether a container of the Compose service runs now.
// An error from Docker counts as not running: nothing is recreated blind.
func serviceRunning(project, service string) bool {
	out, err := exec.Command("docker", "ps", "--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=com.docker.compose.service="+service, "--format", "{{.ID}}").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// ApplyEnv applies the overrides of services as one run of command "env"
// (add-env-overrides D3): a container service is recreated alone (`up -d
// --no-deps`), a running host app is restarted, a host app that is not
// running is left for its next start. One step per service, output redacted.
// A failed step stops the run; the overrides file stays as written. The
// caller holds no instance lock: each step takes the one it needs.
func ApplyEnv(out io.Writer, trigger, root, branch, project, instanceDir, newBase, legacyBase string, services []string) (int, error) {
	info, err := LoadAndValidateManifest(instanceDir, root, branch, project, "apply")
	if err != nil {
		return 0, err
	}
	rec, err := runs.Begin(instanceDir, runs.Meta{Command: "env", Trigger: trigger, Project: project, Branch: branch, Out: out,
		Secrets: func() []redact.Secret { return OutputSecrets(instanceDir, info, nil) }})
	if err != nil {
		return 0, err
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
	for _, j := range jobs {
		var serr error
		code := 0
		switch {
		case isContainer[j.svc] && !serviceRunning(project, j.svc):
			// up -d would start a stopped service alone; the override waits for the next up instead.
			reason := "not running; applies at next up"
			fmt.Fprintf(out, "%s: %s\n", j.svc, reason)
			j.step.Skip(reason)
			continue
		case isContainer[j.svc]:
			w := j.step.Start()
			serr = func() error {
				lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
				if err != nil {
					return err
				}
				defer lock.Release()
				fmt.Fprintf(w, "recreating %s\n", j.svc)
				c, err := Execute(w, w, "apply", root, branch, project, instanceDir, newBase, legacyBase, Options{Service: j.svc})
				code = c
				if err == nil && c != 0 {
					err = fmt.Errorf("apply exited with status %d", c)
				}
				return err
			}()
			j.step.End(serr, code)
		case isHost[j.svc]:
			if proc, ok := info.HostProcs[j.svc]; !ok || !hostRunning(proc) {
				reason := "not running; applies at next start"
				fmt.Fprintf(out, "%s: %s\n", j.svc, reason)
				j.step.Skip(reason)
				continue
			}
			w := j.step.Start()
			fmt.Fprintf(w, "restarting %s\n", j.svc)
			serr = HostRestart(w, root, instanceDir, j.svc, newBase, legacyBase)
			j.step.End(serr, 0)
		default:
			j.step.Skip("not a service of this stack or a host app")
			continue
		}
		if serr != nil {
			err := fmt.Errorf("step %s failed: %v; the override stays as written (`stackfork env unset` reverts it)", j.step.ID, serr)
			rec.Finish(err)
			return 1, nil
		}
	}
	rec.Finish(nil)
	return 0, nil
}
