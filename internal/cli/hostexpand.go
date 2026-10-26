// hostexpand.go expands the placeholders of a host command ({{port}},
// {{url}}, {{hostname}}, {{APP.port}}, {{APP.url}}, {{service.NAME.port}},
// {{service.NAME.url}}) right before it runs. The plan, and so the trust
// hash, keeps the text as written; only the argv handed to exec changes.
package cli

import (
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// expandHostCommand is argv with its placeholders replaced. name is the host
// app the command starts ("" for an exec step) and selfPort its port when a
// flag fixed it (0: the instance's stable one). A command with no placeholder
// comes back untouched and touches nothing. Otherwise every host app of the
// plan gets its port now, persisted in the manifest's host_ports, so
// {{other.port}} works before other starts.
//
// allocate says whether it may give the apps ports, which takes the instance
// lock. A host exec step runs with that lock already held (up, seed), so it
// must not allocate: an app with no port is an error that names it.
func expandHostCommand(argv []string, name string, selfPort int, allocate bool, root, project, instanceDir, newBase, legacyBase string) ([]string, error) {
	if !hasPlaceholder(argv) {
		return argv, nil
	}
	sc, err := hostScope(name, selfPort, allocate, argv, root, project, instanceDir, newBase, legacyBase)
	if err != nil {
		return nil, err
	}
	return sc.Expand(argv)
}

func hasPlaceholder(argv []string) bool {
	for _, a := range argv {
		if knowledge.HasPlaceholder(a) {
			return true
		}
	}
	return false
}

// hostScope reads the instance for Expand. allocate gives every host app of
// the plan a port (a read-only view leaves them 0, so the placeholder errors
// by name). Services are read only when argv names one: that costs a docker call.
func hostScope(self string, selfPort int, allocate bool, argv []string, root, project, instanceDir, newBase, legacyBase string) (knowledge.Scope, error) {
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return knowledge.Scope{}, err
	}
	hostName := ""
	if m != nil {
		hostName = m.Hostname
	} else if allocate {
		return knowledge.Scope{}, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	hostname, err := identity.BrowserHostname(project, hostName)
	if err != nil {
		return knowledge.Scope{}, err
	}
	literal := planHostApps(root) // every host app of the plan -> its fixed port, if the step has one
	if m != nil {
		for n := range m.HostPorts {
			if _, ok := literal[n]; !ok {
				literal[n] = 0
			}
		}
	}
	if self != "" {
		// the port registerHostRoute will use: the flag, else the remembered
		// or allocated one; the plan's port: only reaches it as a flag
		literal[self] = selfPort
	}
	if allocate {
		if m, err = allocateHostPorts(literal, project, instanceDir, newBase, legacyBase); err != nil {
			return knowledge.Scope{}, err
		}
	}
	sc := knowledge.Scope{Self: self, Hostname: hostname, Apps: map[string]int{}}
	for n, p := range literal {
		if p == 0 && m != nil {
			p = m.HostPorts[n]
		}
		sc.Apps[n] = p
	}
	if wantsService(argv) {
		if sc.Services, err = serviceAddrs(project, newBase, legacyBase); err != nil {
			return knowledge.Scope{}, err
		}
	}
	return sc, nil
}

// planHostApps maps each host step of the checkout's plan to its literal port (0: none).
func planHostApps(root string) map[string]int {
	apps := map[string]int{}
	if k, _ := knowledge.LoadForCheckout(root); k != nil {
		for _, s := range k.Plan.HostSteps() {
			if s.Name != "" {
				apps[s.Name] = s.Port
			}
		}
	}
	return apps
}

// allocatePlanHostPorts gives every host app of the plan its port now, when
// a host-run step of the plan uses a placeholder, so {{APP.port}} works in a
// host exec step before APP starts. Call it where the instance lock is not
// held. It is best effort: a step that needs a port reports the miss itself.
func allocatePlanHostPorts(plan knowledge.Plan, root, project, instanceDir, newBase, legacyBase string) {
	uses := false
	for _, s := range plan.HostRunSteps() {
		if argv, err := knowledge.SplitArgv(s.Cmd); err == nil && hasPlaceholder(argv) {
			uses = true
		}
	}
	if !uses {
		return
	}
	apps := planHostApps(root)
	if len(apps) > 0 {
		allocateHostPorts(apps, project, instanceDir, newBase, legacyBase) //nolint:errcheck // see above
	}
}

func wantsService(argv []string) bool {
	for _, a := range argv {
		if strings.Contains(a, "service.") && knowledge.HasPlaceholder(a) {
			return true
		}
	}
	return false
}

// allocateHostPorts gives each app in apps with no fixed port (value 0) and
// no remembered one a stable free port, all under one hold of the instance
// lock, and returns the manifest as written.
func allocateHostPorts(apps map[string]int, project, instanceDir, newBase, legacyBase string) (*state.Manifest, error) {
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	path := filepath.Join(instanceDir, "manifest.json")
	m, err := state.ReadManifest(path)
	if err != nil || m == nil {
		return nil, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	var names []string
	for n, p := range apps {
		if p == 0 && m.HostPorts[n] == 0 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return m, nil
	}
	if m.HostPorts == nil {
		m.HostPorts = map[string]int{}
	}
	for _, n := range names {
		p, err := pickHostPort(m, n, project, newBase, legacyBase)
		if err != nil {
			return nil, err
		}
		m.HostPorts[n] = p
	}
	return m, state.WritePrivate(path, m)
}

// pickHostPort is the stable free port for the host app name: one no other
// instance, service or host app of this one holds.
func pickHostPort(info *state.Manifest, name, project, newBase, legacyBase string) (int, error) {
	taken, err := recordedPorts(newBase, legacyBase, project)
	if err != nil {
		return 0, err
	}
	for _, a := range info.Ports {
		taken[a.Host] = true
	}
	for _, p := range info.HostPorts {
		taken[p] = true
	}
	return ports.Allocate(project, "host/"+name, 0, ports.RangeFromEnv(), func(p int) bool {
		return taken[p] || ports.Bound(p)
	})
}

var portInURL = regexp.MustCompile(`:(\d+)(?:/|$)`)

// serviceAddrs is, per Compose service, the first published address the way
// `urls` shows it (the direct one, not the proxied one).
func serviceAddrs(project, newBase, legacyBase string) (map[string]knowledge.ServiceAddr, error) {
	inv, err := inventory.Collect(inventory.Options{NewStateBase: newBase, LegacyStateBase: legacyBase, Version: Version})
	if err != nil {
		return nil, err
	}
	out := map[string]knowledge.ServiceAddr{}
	for _, in := range inv.Instances {
		if in.Project != project {
			continue
		}
		for _, s := range in.Services {
			if s.Host != nil {
				continue
			}
			for _, e := range s.Endpoints {
				if !e.Known || e.Proxied || e.URL == "" {
					continue
				}
				a := knowledge.ServiceAddr{URL: e.URL}
				if m := portInURL.FindStringSubmatch(e.URL); m != nil {
					a.Port, _ = strconv.Atoi(m[1])
				}
				out[s.Name] = a
				break
			}
		}
	}
	return out, nil
}

// expandedText is cmd with its placeholders replaced for the checkout, or the
// reason it cannot be. It reads the instance and allocates nothing.
func expandedText(cmd, self string, root, project, instanceDir, newBase, legacyBase string) (string, error) {
	argv, err := knowledge.SplitArgv(cmd)
	if err != nil {
		return "", err
	}
	sc, err := hostScope(self, 0, false, argv, root, project, instanceDir, newBase, legacyBase)
	if err != nil {
		return "", err
	}
	out, err := sc.Expand(argv)
	if err != nil {
		return "", err
	}
	return strings.Join(out, " "), nil
}

// hostnameOf is the instance hostname for a manifest, or "" when none is valid.
func hostnameOf(project string, m *state.Manifest) string {
	h := ""
	if m != nil {
		h = m.Hostname
	}
	h, _ = identity.BrowserHostname(project, h)
	return h
}

// checkoutHostname is the hostname of root's instance, "" outside a checkout.
func checkoutHostname(root, newBase, legacyBase string) string {
	_, project, _, m, err := checkoutManifest(root, newBase, legacyBase)
	if err != nil {
		return ""
	}
	return hostnameOf(project, m)
}
