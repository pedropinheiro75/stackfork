package lifecycle

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// hostPrefix marks a host app among the node names of the dependency graph
// and in a host step's depends_on; a Compose service name has no colon.
const hostPrefix = "host:"

// composeDeps maps each service of the instance snapshot to the services its
// Compose depends_on names; nil when the snapshot cannot be read.
func composeDeps(instanceDir string) map[string][]string {
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil
	}
	parsed, err := compose.DecodeJSON(data)
	if err != nil {
		return nil
	}
	top, _ := parsed.(map[string]interface{})
	svcs, _ := top["services"].(map[string]interface{})
	out := make(map[string][]string, len(svcs))
	for name, v := range svcs {
		sv, _ := v.(map[string]interface{})
		out[name] = dependsOn(sv["depends_on"])
	}
	return out
}

// oneShots are the services of the instance snapshot that finish by design:
// one another service awaits with condition service_completed_successfully,
// or one with no ports and no healthcheck (a migrate or init job). A server
// (ports or a healthcheck) that exited 0 was stopped, so it is not here.
func oneShots(instanceDir string) map[string]bool {
	top, _ := snapshotModel(instanceDir).(map[string]interface{})
	svcs, _ := top["services"].(map[string]interface{})
	out := map[string]bool{}
	for name, raw := range svcs {
		m, _ := raw.(map[string]interface{})
		if ports, _ := m["ports"].([]interface{}); len(ports) == 0 && m["healthcheck"] == nil {
			out[name] = true
		}
		cond, _ := m["depends_on"].(map[string]interface{})
		for d, v := range cond {
			if c, _ := v.(map[string]interface{}); c != nil && c["condition"] == "service_completed_successfully" {
				out[d] = true
			}
		}
	}
	return out
}

// Dependents returns everything that needs one of targets, directly or not:
// Compose services (from their depends_on) and host apps (from the plan's
// depends_on), each sorted. A target is a service name or "host:NAME" and is
// never in the result. A depends_on name that is neither a host app nor a
// service of the snapshot is ignored. Cycles end: each node is visited once.
func Dependents(plan knowledge.Plan, deps map[string][]string, targets []string) (services, hosts []string) {
	needs := map[string][]string{} // node -> the nodes it needs
	for svc, ds := range deps {
		for _, d := range ds {
			if _, ok := deps[d]; ok {
				needs[svc] = append(needs[svc], d)
			}
		}
	}
	apps := map[string]bool{}
	for _, s := range plan.HostSteps() {
		apps[s.Name] = true
	}
	for _, s := range plan.HostSteps() {
		node := hostPrefix + s.Name
		for _, d := range s.DependsOn {
			if name, ok := strings.CutPrefix(d, hostPrefix); ok {
				if apps[name] {
					needs[node] = append(needs[node], d)
				}
				continue
			}
			// a bare name is a host app, and a service too when the stack has one of that name
			if apps[d] {
				needs[node] = append(needs[node], hostPrefix+d)
			}
			if _, ok := deps[d]; ok {
				needs[node] = append(needs[node], d)
			}
		}
	}
	seen := map[string]bool{}
	for _, t := range targets {
		seen[t] = true
	}
	for grew := true; grew; {
		grew = false
		for node, ns := range needs {
			if seen[node] {
				continue
			}
			for _, n := range ns {
				if seen[n] {
					seen[node], grew = true, true
					if name, ok := strings.CutPrefix(node, hostPrefix); ok {
						hosts = append(hosts, name)
					} else {
						services = append(services, node)
					}
					break
				}
			}
		}
	}
	sort.Strings(services)
	sort.Strings(hosts)
	return services, hosts
}

// CascadeFor is Dependents when the checkout's plan has stop_cascade on, and
// nothing otherwise (also when the knowledge cannot be read: a stop then
// does what it always did).
func CascadeFor(root, instanceDir string, targets []string) (services, hosts []string) {
	k, err := knowledge.LoadForCheckout(root)
	if err != nil || k == nil || !k.Plan.StopCascade {
		return nil, nil
	}
	return Dependents(k.Plan, composeDeps(instanceDir), targets)
}

// stopRunningHosts stops, with keep, each of names that runs, in order, and
// returns those it stopped. The caller holds the instance lock. The first
// failure ends it: what depends on a thing that would not stop is not stopped.
func stopRunningHosts(stderr io.Writer, instanceDir string, names []string) (stopped []string, err error) {
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil || m == nil {
		return nil, err
	}
	for _, n := range names {
		if proc, ok := m.HostProcs[n]; !ok || !state.HostIdentity(proc) {
			continue
		}
		if err := stopHost(stderr, instanceDir, n, HostStopTimeout(), true, false); err != nil {
			return stopped, err
		}
		stopped = append(stopped, n)
	}
	return stopped, nil
}

// cascadeLine is the line printed after a cascading stop.
func cascadeLine(targets, dependents []string) string {
	what := "its"
	if len(targets) > 1 {
		what = "their"
	}
	return fmt.Sprintf("stopped %s and %s %s%s", strings.Join(targets, ", "), what, "dependents: ", strings.Join(dependents, ", "))
}

// StopHostCascadeLocked is StopHostLocked that, with stop_cascade on, first
// stops the running host apps that depend on name. It returns their names.
func StopHostCascadeLocked(stderr io.Writer, root, instanceDir, name, newBase, legacyBase string) ([]string, error) {
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return nil, err
	}
	_, hosts := CascadeFor(root, instanceDir, []string{hostPrefix + name})
	stopped, err := stopRunningHosts(stderr, instanceDir, hosts)
	if err == nil {
		err = StopHost(stderr, instanceDir, name, HostStopTimeout(), true)
	}
	lock.Release()
	ReloadProxyIfRunning(stderr, newBase, legacyBase)
	return stopped, err
}

// stopCascadeHosts is stopRunningHosts under the instance lock: the caller's
// when locked, else one taken for the call.
func stopCascadeHosts(stderr io.Writer, instanceDir string, names []string, locked bool) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if !locked {
		lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
		if err != nil {
			return nil, err
		}
		defer lock.Release()
	}
	return stopRunningHosts(stderr, instanceDir, names)
}
