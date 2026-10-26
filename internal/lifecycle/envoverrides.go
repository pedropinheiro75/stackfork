package lifecycle

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// DerivedOverridesFile is the second Compose file `Execute` passes after the
// snapshot (add-env-overrides D2). The snapshot and its hash never change.
const DerivedOverridesFile = "compose.env-overrides.json"

// derivedOverrides writes <instanceDir>/compose.env-overrides.json from the
// instance's overrides, limited to services of the snapshot, and returns its
// path. With nothing to apply it removes the file and returns "". A `$` is
// doubled: Compose would interpolate it in a file it reads.
func derivedOverrides(instanceDir string) (string, error) {
	path := filepath.Join(instanceDir, DerivedOverridesFile)
	o, err := state.ReadEnvOverrides(instanceDir)
	if err != nil {
		return "", err
	}
	known := map[string]bool{}
	for _, n := range SnapshotServices(instanceDir) {
		known[n] = true
	}
	services := map[string]map[string]map[string]string{}
	for svc, vars := range o.Services {
		if !known[svc] || len(vars) == 0 {
			continue
		}
		env := make(map[string]string, len(vars))
		for n, v := range vars {
			env[n] = strings.ReplaceAll(v, "$", "$$")
		}
		services[svc] = map[string]map[string]string{"environment": env}
	}
	if len(services) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return "", nil
	}
	return path, state.WritePrivate(path, map[string]any{"services": services})
}

// EnvTargets splits a service name into what an override on it means: a
// Compose service of the snapshot, a host app, or neither. A name in both is
// the one that runs in the instance's current mode: the host app when the
// plan's hybrid mode has a host step of that name, else the container (the
// same rule StartHostApps uses).
func EnvTargets(instanceDir string, m *state.Manifest) (containers, hosts []string) {
	inStack := map[string]bool{}
	replaced := hybridHostNames(m) // host steps standing in for a Compose service of the same name
	for _, n := range SnapshotServices(instanceDir) {
		inStack[n] = !replaced[n]
		if !replaced[n] {
			containers = append(containers, n)
		}
	}
	seen := map[string]bool{}
	if m != nil {
		for n := range m.HostProcs {
			seen[n] = true
		}
		for n := range m.HostPorts {
			seen[n] = true
		}
	}
	// A host step of the plan is a target before it ever ran (D1).
	if m != nil && m.Root != "" {
		main := identity.MainRepoOf(m.Root)
		if main == "" {
			main = m.Root
		}
		plan := LoadPlan(m.Root, main)
		for _, mode := range []string{knowledge.ModeContainer, knowledge.ModeHybrid} {
			steps, _ := plan.ForMode(mode)
			for _, st := range steps {
				if st.Kind == knowledge.KindHost && st.Name != "" && !inStack[st.Name] {
					seen[st.Name] = true
				}
			}
		}
	}
	for n := range seen {
		if !inStack[n] {
			hosts = append(hosts, n)
		}
	}
	sort.Strings(hosts)
	return containers, hosts
}

// hybridHostNames are the host steps of the plan's current mode when that
// mode is hybrid: each one replaces the Compose service of its name.
func hybridHostNames(m *state.Manifest) map[string]bool {
	out := map[string]bool{}
	if m == nil || m.Root == "" {
		return out
	}
	main := identity.MainRepoOf(m.Root)
	if main == "" {
		main = m.Root
	}
	if PlanMode(m.Root, main) != knowledge.ModeHybrid {
		return out
	}
	run, _ := LoadPlan(m.Root, main).ForMode(knowledge.ModeHybrid)
	for _, st := range run {
		if st.Kind == knowledge.KindHost && st.Name != "" {
			out[st.Name] = true
		}
	}
	return out
}

// EnvOverrideSecrets are the stored override values that must never reach
// output: those of secret-like names (stored with --allow-secret), and of any
// name that is a secret reference of the instance.
func EnvOverrideSecrets(instanceDir string) []redact.Secret {
	o, err := state.ReadEnvOverrides(instanceDir)
	if err != nil || o.Count() == 0 {
		return nil
	}
	m, _ := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	var out []redact.Secret
	for svc, vars := range o.Services {
		for n, v := range vars {
			if SecretLikeEnvName(m, n) && v != "" {
				out = append(out, redact.Secret{Name: svc + "/" + n, Value: v})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SecretLikeEnvName is redact.SecretLikeName plus the instance's own secret
// references (the manifest names and their env aliases, add-env-overrides D5).
func SecretLikeEnvName(m *state.Manifest, name string) bool {
	if redact.SecretLikeName(name) {
		return true
	}
	if m == nil {
		return false
	}
	for n := range m.ResolvedSecrets() {
		for _, alias := range compose.SecretEnvAliases(n) {
			if alias == name {
				return true
			}
		}
	}
	return false
}
