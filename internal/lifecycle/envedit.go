package lifecycle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SetEnvOverrides stores and removes overrides of one service in one write,
// under the instance lock (add-env-overrides D1, D5, D9). The service must be
// a Compose service of the snapshot or a host app of the instance. A
// secret-like name is refused unless allowSecret (people only). Nothing is
// written when any part is refused. It applies nothing: see ApplyEnv.
func SetEnvOverrides(instanceDir, service string, set map[string]string, unset []string, allowSecret bool) error {
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return err
	}
	containers, hosts := EnvTargets(instanceDir, m)
	valid := append(append([]string{}, containers...), hosts...)
	sort.Strings(valid)
	known := false
	for _, n := range valid {
		known = known || n == service
	}
	if !known {
		return fmt.Errorf("unknown service %q; valid names: %s", service, strings.Join(valid, ", "))
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := state.ValidEnvName(n); err != nil {
			return err
		}
		if !allowSecret && SecretLikeEnvName(m, n) {
			return fmt.Errorf("%s looks like a secret; store it with stackfork secret and let the service read the reference", n)
		}
	}
	if vault := vaultInterpolated(instanceDir, m, service); len(vault) > 0 {
		for _, n := range names {
			if vault[n] {
				return fmt.Errorf("%s gets its value from a secret of this instance; change that secret under Settings → Secrets (stackfork secret map)", n)
			}
		}
	}
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return err
	}
	defer lock.Release()
	o, err := state.ReadEnvOverrides(instanceDir)
	if err != nil {
		return err
	}
	for _, n := range unset {
		if !o.Unset(service, n) {
			return fmt.Errorf("no override %s on %s", n, service)
		}
	}
	for _, n := range names {
		if err := o.Set(service, n, set[n]); err != nil {
			return err
		}
	}
	return o.Write(instanceDir)
}

// vaultInterpolated are the names of a container service whose snapshot value
// interpolates a secret of the instance (`${NPM_TOKEN}`): an override would
// hide the reference, so they are not overridable.
func vaultInterpolated(instanceDir string, m *state.Manifest, service string) map[string]bool {
	b, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil || m == nil {
		return nil
	}
	parsed, err := compose.DecodeJSON(b)
	if err != nil {
		return nil
	}
	top, _ := parsed.(map[string]any)
	svcs, _ := top["services"].(map[string]any)
	cfg, ok := svcs[service].(map[string]any)
	if !ok {
		return nil
	}
	vault := map[string]bool{}
	for n := range m.ResolvedSecrets() {
		for _, a := range compose.SecretEnvAliases(n) {
			vault[a] = true
		}
	}
	out := map[string]bool{}
	for k, v := range projectEnv(cfg, m.Root, vault) {
		if v.vault && v.origin == "project" {
			out[k] = true
		}
	}
	return out
}

// ClearEnvOverrides removes the overrides of one service, or of every service
// when service is empty. It returns the services it touched and how many
// variables it removed.
func ClearEnvOverrides(instanceDir, service string) (touched []string, removed int, err error) {
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		return nil, 0, err
	}
	defer lock.Release()
	o, err := state.ReadEnvOverrides(instanceDir)
	if err != nil {
		return nil, 0, err
	}
	for svc, vars := range o.Services {
		if service != "" && svc != service {
			continue
		}
		touched, removed = append(touched, svc), removed+len(vars)
		o.ClearService(svc)
	}
	sort.Strings(touched)
	return touched, removed, o.Write(instanceDir)
}

// EnvVar is one variable a service receives, as the dashboard and `env
// overrides --json` show it. A secret-like name, a vault reference and a
// value of a masked override carry no value: Masked is true and Value null.
type EnvVar struct {
	Name   string  `json:"name"`
	Value  *string `json:"value"`  // the effective value; null when Masked
	Masked bool    `json:"masked"` // secret-like name or vault reference
	// Origin is where the effective value comes from: "override", "project"
	// (the snapshot's environment), "env_file" or "vault" (a secret reference).
	Origin string `json:"origin"`
	// HasDefault: the project defines this name, so removing the override
	// restores a value. Default is that value (null when masked or absent).
	HasDefault bool    `json:"has_default"`
	Default    *string `json:"default"`
}

// EnvService is the environment of one service for the Environment panel.
// Kind is "container" (a Compose service of the snapshot), "host" (a host app;
// only its overrides are listed) or "unknown" (overrides of a service the
// snapshot and the host apps no longer have).
type EnvService struct {
	Name string   `json:"name"`
	Kind string   `json:"kind"`
	Vars []EnvVar `json:"vars"`
	// Build: the service has a build key, so a VITE_* value applies at the next build.
	Build bool `json:"build,omitempty"`
}

var (
	urlPassword = regexp.MustCompile(`(://[^:/@\s]*:)\S*@`)
	varRef      = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)[^}]*\}`)
)

// maskURLPassword hides the password of every user:password@host in s. The
// password runs to the last @ of its word, so a password that holds a / or an
// @ is hidden whole.
func maskURLPassword(s string) string { return urlPassword.ReplaceAllString(s, "${1}****@") }

// EnvView reads the environment every service of the instance receives
// (add-env-overrides D6): the snapshot's environment, the names of its
// env_file entries and the overrides. It never reads the vault and shows no
// value of a secret-like name or a vault reference; a password inside a URL
// is replaced by ****. A service with no variables is listed with none.
func EnvView(instanceDir string) ([]EnvService, error) {
	m, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	o, err := state.ReadEnvOverrides(instanceDir)
	if err != nil {
		return nil, err
	}
	containers, hosts := EnvTargets(instanceDir, m)
	var snapServices map[string]any
	if b, err := os.ReadFile(filepath.Join(instanceDir, "compose.json")); err == nil {
		if parsed, err := compose.DecodeJSON(b); err == nil {
			top, _ := parsed.(map[string]any)
			snapServices, _ = top["services"].(map[string]any)
		}
	}
	vault := map[string]bool{}
	if m != nil {
		for n := range m.ResolvedSecrets() {
			for _, a := range compose.SecretEnvAliases(n) {
				vault[a] = true
			}
		}
	}
	root := ""
	if m != nil {
		root = m.Root
	}

	build := func(name, kind string, project map[string]projectVar) EnvService {
		svc := EnvService{Name: name, Kind: kind, Vars: []EnvVar{}}
		ov := o.Services[name]
		names := map[string]bool{}
		for n := range project {
			names[n] = true
		}
		for n := range ov {
			names[n] = true
		}
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			p, hasP := project[n]
			v := EnvVar{Name: n, HasDefault: hasP}
			secretName := SecretLikeEnvName(m, n)
			pMasked := hasP && (secretName || p.vault)
			if hasP && !pMasked {
				s := maskURLPassword(p.value)
				v.Default = &s
			}
			if val, ok := ov[n]; ok {
				v.Origin = "override"
				if secretName {
					v.Masked = true
				} else {
					s := val
					v.Value = &s
				}
			} else {
				v.Origin = p.origin
				if p.vault {
					v.Origin = "vault"
				}
				v.Masked = pMasked
				if !pMasked {
					v.Value = v.Default
				}
			}
			svc.Vars = append(svc.Vars, v)
		}
		return svc
	}

	var out []EnvService
	for _, name := range containers {
		cfg, _ := snapServices[name].(map[string]any)
		svc := build(name, "container", projectEnv(cfg, root, vault))
		_, svc.Build = cfg["build"]
		out = append(out, svc)
	}
	for _, name := range hosts {
		out = append(out, build(name, "host", nil))
	}
	known := map[string]bool{}
	for _, s := range out {
		known[s.Name] = true
	}
	var unknown []string
	for svc := range o.Services {
		if !known[svc] {
			unknown = append(unknown, svc)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		out = append(out, build(name, "unknown", nil))
	}
	return out, nil
}

// projectVar is one variable the project gives a service.
type projectVar struct {
	value  string
	origin string // "project" or "env_file"
	vault  bool   // the value is a reference to a secret of the instance
}

// projectEnv is what the snapshot gives a service: the names of its env_file
// entries, then its environment on top (Compose ranks environment higher).
func projectEnv(cfg map[string]any, root string, vault map[string]bool) map[string]projectVar {
	out := map[string]projectVar{}
	for _, f := range envFilePaths(cfg["env_file"]) {
		if !filepath.IsAbs(f) {
			f = filepath.Join(root, f)
		}
		for k, v := range readDotenv(f) {
			out[k] = projectVar{value: v, origin: "env_file"}
		}
	}
	switch env := cfg["environment"].(type) {
	case map[string]any:
		for k, raw := range env {
			v, _ := raw.(string)
			out[k] = projectVar{value: v, origin: "project", vault: isVaultRef(k, v, vault)}
		}
	case []any: // KEY=VALUE list, in case a snapshot keeps the list form
		for _, raw := range env {
			if s, ok := raw.(string); ok {
				k, v, _ := strings.Cut(s, "=")
				out[k] = projectVar{value: v, origin: "project", vault: isVaultRef(k, v, vault)}
			}
		}
	}
	return out
}

// isVaultRef: the name is one a secret of the instance is set under, or the
// value interpolates one (`${NPM_TOKEN}` survives in the snapshot).
func isVaultRef(name, value string, vault map[string]bool) bool {
	if vault[name] {
		return true
	}
	for _, m := range varRef.FindAllStringSubmatch(value, -1) {
		if vault[m[1]] {
			return true
		}
	}
	return false
}

func envFilePaths(raw any) []string {
	var out []string
	switch v := raw.(type) {
	case string:
		out = append(out, v)
	case []any:
		for _, e := range v {
			switch x := e.(type) {
			case string:
				out = append(out, x)
			case map[string]any:
				if p, ok := x["path"].(string); ok {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// readDotenv reads KEY=VALUE lines of an env file the project supplied, for
// display only. A file that cannot be read gives nothing.
func readDotenv(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || state.ValidEnvName(k) != nil {
			continue
		}
		v = strings.TrimSpace(v)
		if n := len(v); n >= 2 && (v[0] == '"' || v[0] == '\'') && v[n-1] == v[0] {
			v = v[1 : n-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[k] = v
	}
	return out
}
