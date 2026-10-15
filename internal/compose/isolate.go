package compose

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// Finding is one thing prepare's isolation step would refuse or rewrite,
// collected by IsolateDryRun instead of stopping at the first (task 13.3).
// Kind is a stable category ("container_name", "external_resource", ...),
// Target names the resource in the source config, Action is "refuse" or
// "rewrite", and Reason is the human-readable explanation (a refusal's
// Reason is the exact error Isolate would return).
type Finding struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

const (
	actionRefuse  = "refuse"
	actionRewrite = "rewrite"
)

// Isolate mirrors Python's isolate(source, root, project, env_file) exactly,
// including error messages. source is JSON-shaped (map[string]interface{}
// etc, as produced by DecodeJSON); root need not be pre-resolved. envFile
// is "" for Python's None. It fails at the first refusal, unchanged by
// IsolateDryRun's addition (task 13.3: "don't change prepare's behaviour").
func Isolate(source interface{}, root, project, envFile string) (interface{}, error) {
	return isolate(source, root, project, envFile, nil)
}

// IsolateDryRun runs the same isolation logic as Isolate in collect-all
// mode: every refusal and rewrite (container_name removed, published port
// stripped, volume/network renamed, image renamed for builds, ...) is
// recorded with a reason instead of stopping at the first one found. It
// still fails outright on a structural problem (bad root, config not a
// mapping) since that leaves nothing meaningful to scan. Used by
// `stackfork learn` to report what `prepare` would do without doing it.
func IsolateDryRun(source interface{}, root, project, envFile string) ([]Finding, error) {
	var findings []Finding
	_, err := isolate(source, root, project, envFile, &findings)
	return findings, err
}

// isolate is the shared implementation behind Isolate and IsolateDryRun.
// findings == nil means fail-fast (Isolate): the first refusal is returned
// as an error. findings != nil means collect-all (IsolateDryRun): refusals
// and rewrites are appended to *findings and processing continues.
func isolate(source interface{}, root, project, envFile string, findings *[]Finding) (interface{}, error) {
	rootResolved, err := identity.ResolvePath(root)
	if err != nil {
		return nil, err
	}
	var envFileResolved string
	if envFile != "" {
		envFileResolved, err = identity.ResolvePath(envFile)
		if err != nil {
			return nil, err
		}
	}

	collect := findings != nil
	// fail records a refusal. Collect-all mode: appends and returns nil (so
	// the caller continues past it). Fail-fast mode: returns the error the
	// caller must propagate immediately, exactly as before this function was
	// split out of Isolate.
	fail := func(kind, target, reason string) error {
		if collect {
			*findings = append(*findings, Finding{Kind: kind, Target: target, Action: actionRefuse, Reason: reason})
			return nil
		}
		return errors.New(reason)
	}
	// rewrite records a change Isolate always makes when applicable; a no-op
	// outside collect-all mode.
	rewrite := func(kind, target, reason string) {
		if collect {
			*findings = append(*findings, Finding{Kind: kind, Target: target, Action: actionRewrite, Reason: reason})
		}
	}

	model, ok := asMap(deepCopy(source))
	if !ok {
		return nil, fmt.Errorf("compose config is not a mapping")
	}
	model["name"] = project

	for _, kind := range []string{"volumes", "networks", "secrets", "configs"} {
		section, ok := asMap(model[kind])
		if !ok {
			continue
		}
		for name, raw := range section {
			definition, ok := asMap(raw)
			if !ok {
				definition = map[string]interface{}{}
				section[name] = definition
			}
			target := kind + "." + name
			if truthy(definition["external"]) {
				if err := fail("external_resource", target, fmt.Sprintf("%s.%s: external resource requires a project-specific replacement", kind, name)); err != nil {
					return nil, err
				}
				continue
			}
			driver, _ := definition["driver"].(string)
			driverOK := definition["driver"] == nil || driver == "local" || driver == "bridge"
			if truthy(definition["driver_opts"]) || !driverOK {
				if err := fail("custom_driver", target, fmt.Sprintf("%s.%s: custom driver/storage needs review", kind, name)); err != nil {
					return nil, err
				}
				continue
			}
			if kind == "networks" {
				if ipam, ok := asMap(definition["ipam"]); ok && truthy(ipam["config"]) {
					if err := fail("fixed_subnet", target, fmt.Sprintf("networks.%s: fixed subnets must be removed for concurrent stacks", name)); err != nil {
						return nil, err
					}
					continue
				}
			}
			if kind == "volumes" || kind == "networks" {
				scopedName := fmt.Sprintf("%s_%s", project, name)
				definition["name"] = scopedName
				rewrite("renamed_"+strings.TrimSuffix(kind, "s"), target, fmt.Sprintf("%s: renamed to %s so concurrent stacks don't collide", target, scopedName))
			}
		}
	}

	services, _ := asMap(model["services"])
	for name, raw := range services {
		service, ok := asMap(raw)
		if !ok {
			continue
		}
		labels, ok := asMap(service["labels"])
		if !ok {
			labels = map[string]interface{}{}
			service["labels"] = labels
		}
		labels[identity.LabelRoot] = strings.ReplaceAll(rootResolved, "$", "$$")

		for _, key := range []string{"privileged", "devices", "volumes_from", "external_links",
			"provider", "develop", "post_start", "pre_start", "pre_stop"} {
			if truthy(service[key]) {
				if err := fail("unsafe_key", fmt.Sprintf("services.%s.%s", name, key), fmt.Sprintf("%s.%s: cannot isolate automatically", name, key)); err != nil {
					return nil, err
				}
			}
		}
		for _, key := range []string{"network_mode", "pid", "ipc"} {
			v := service[key]
			if v == nil {
				continue
			}
			s, isString := v.(string)
			if !isString || (s != "none" && s != "private") {
				if err := fail("shared_namespace", fmt.Sprintf("services.%s.%s", name, key), fmt.Sprintf("%s.%s: shared namespace requires a project-specific replacement", name, key)); err != nil {
					return nil, err
				}
			}
		}

		if truthy(service["container_name"]) {
			rewrite("container_name", fmt.Sprintf("services.%s", name), fmt.Sprintf("services.%s: container_name removed so concurrent stacks don't collide", name))
		}
		delete(service, "container_name")

		if truthy(service["build"]) {
			image := fmt.Sprintf("%s-%s:dev", project, name)
			rewrite("build_image", fmt.Sprintf("services.%s", name), fmt.Sprintf("services.%s: image renamed to %s for the isolated build", name, image))
			service["image"] = image
			if build, ok := asMap(service["build"]); ok {
				delete(build, "tags")
			}
		}

		if ports, ok := asList(service["ports"]); ok {
			for i, raw := range ports {
				port, ok := asMap(raw)
				if !ok || !isJSONInt(port["target"]) {
					if err := fail("unresolved_port", fmt.Sprintf("services.%s.ports[%d]", name, i), fmt.Sprintf("%s: unresolved port mapping", name)); err != nil {
						return nil, err
					}
					continue
				}
				hadPublished := truthy(port["published"])
				delete(port, "published")
				port["host_ip"] = "127.0.0.1"
				port["mode"] = "ingress"
				if hadPublished {
					rewrite("published_port", fmt.Sprintf("services.%s.ports[%d]", name, i), fmt.Sprintf("services.%s: published port stripped so Docker assigns a random host port", name))
				}
			}
		}

		if entries, ok := asList(service["env_file"]); ok {
			for i, raw := range entries {
				var entryPath string
				if m, ok := asMap(raw); ok {
					entryPath, _ = m["path"].(string)
				} else if s, ok := raw.(string); ok {
					entryPath = s
				}
				resolved, err := identity.ResolvePath(entryPath)
				if err != nil {
					return nil, err
				}
				if envFile == "" || resolved != envFileResolved {
					if err := fail("env_file_mismatch", fmt.Sprintf("services.%s.env_file[%d]", name, i), fmt.Sprintf("%s: env_file must reference only the explicitly supplied --env-file; override the original env_file list", name)); err != nil {
						return nil, err
					}
					continue
				}
			}
		}

		if mounts, ok := asList(service["volumes"]); ok {
			for i, raw := range mounts {
				mount, ok := asMap(raw)
				if !ok {
					continue
				}
				if vol := cacheVolumeName(name, mount); vol != "" {
					// An anonymous dependency volume (node_modules, .nuxt...)
					// is new and empty after every down, so each start
					// reinstalled everything. A named one per instance keeps
					// it; down --volumes and prune still remove it.
					top, ok := asMap(model["volumes"])
					if !ok {
						top = map[string]interface{}{}
						model["volumes"] = top
					}
					top[vol] = map[string]interface{}{"name": fmt.Sprintf("%s_%s", project, vol)}
					mount["source"] = vol
					rewrite("cache_volume", fmt.Sprintf("services.%s.volumes[%d]", name, i),
						fmt.Sprintf("services.%s: anonymous %v kept across restarts as volume %s", name, mount["target"], vol))
					continue
				}
				if t, _ := mount["type"].(string); t != "bind" {
					continue
				}
				mountTarget := fmt.Sprintf("services.%s.volumes[%d]", name, i)
				source, _ := mount["source"].(string)
				path, err := identity.ResolvePath(source)
				if err != nil {
					return nil, err
				}
				if strings.HasSuffix(filepath.Base(path), ".sock") || path == "/proc" || path == "/sys" || path == "/dev" {
					if err := fail("socket_mount", mountTarget, fmt.Sprintf("%s: host control mount is not isolated", name)); err != nil {
						return nil, err
					}
					continue
				}
				if _, err := os.Stat(path); err != nil {
					if err := fail("missing_bind_source", mountTarget, fmt.Sprintf("%s: bind source does not exist: %s", name, path)); err != nil {
						return nil, err
					}
					continue
				}
				if !truthy(mount["read_only"]) && !isRelativeTo(path, rootResolved) {
					if err := fail("writable_bind_outside_checkout", mountTarget, fmt.Sprintf("%s: writable bind outside checkout: %s", name, path)); err != nil {
						return nil, err
					}
					continue
				}
				bind, ok := asMap(mount["bind"])
				if !ok {
					bind = map[string]interface{}{}
					mount["bind"] = bind
				}
				bind["create_host_path"] = false
			}
		}
	}

	return model, nil
}

// cacheDirs are dependency and build-cache folders: safe to keep between
// restarts of one instance, slow to rebuild from nothing.
var cacheDirs = map[string]bool{
	"node_modules": true, ".nuxt": true, ".next": true, ".output": true, ".svelte-kit": true,
	".pnpm-store": true, ".yarn": true, ".cache": true, "vendor": true, ".venv": true,
	"venv": true, "__pycache__": true, "target": true, ".gradle": true, "deps": true, "_build": true,
}

// cacheVolumeName returns the named volume an anonymous cache mount
// becomes ("" for anything else): <service>_<path with _ for />.
func cacheVolumeName(service string, mount map[string]interface{}) string {
	if t, _ := mount["type"].(string); t != "volume" || truthy(mount["source"]) {
		return ""
	}
	target, _ := mount["target"].(string)
	if !cacheDirs[path.Base(target)] {
		return ""
	}
	slug := strings.Join(strings.FieldsFunc(strings.ToLower(target), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
	}), "_")
	return service + "_" + slug
}

// WireSecrets adds a top-level `secrets: { NAME: { environment: NAME } }`
// entry for each name (task 6.0/6.1, design.md D6) to an already-isolated
// model, and grants each one to the services that need it: a service that
// already references the secret name (short or long `secrets:` syntax)
// keeps that reference; otherwise every service with a `build` is granted
// it (this is how the template's `app` service, whose nuxt.compose.yaml
// declares no `secrets:` of its own, gets npm_token). Only the name is
// ever written here - the value lives solely in the process env passed to
// `docker compose` at up/run. Call it after Isolate, before the model is
// written to the snapshot.
func WireSecrets(model interface{}, names []string) {
	if len(names) == 0 {
		return
	}
	m, ok := asMap(model)
	if !ok {
		return
	}
	services, _ := asMap(m["services"])
	secretsSection, ok := asMap(m["secrets"])
	if !ok {
		secretsSection = map[string]interface{}{}
		m["secrets"] = secretsSection
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		secretsSection[name] = map[string]interface{}{"environment": name}
		targets := explicitSecretConsumers(services, name)
		if len(targets) == 0 {
			targets = servicesWithBuild(services)
		}
		for _, svcName := range targets {
			grantSecret(services, svcName, name)
		}
	}
}

// secretEntryName returns the secret name a service's `secrets:` list entry
// refers to, in either short syntax (a plain string) or long syntax (an
// object with a `source` field).
func secretEntryName(entry interface{}) string {
	if s, ok := entry.(string); ok {
		return s
	}
	if m, ok := asMap(entry); ok {
		if s, ok := m["source"].(string); ok {
			return s
		}
	}
	return ""
}

func explicitSecretConsumers(services map[string]interface{}, name string) []string {
	var names []string
	for svcName, raw := range services {
		svc, ok := asMap(raw)
		if !ok {
			continue
		}
		list, ok := asList(svc["secrets"])
		if !ok {
			continue
		}
		for _, entry := range list {
			if secretEntryName(entry) == name {
				names = append(names, svcName)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

func servicesWithBuild(services map[string]interface{}) []string {
	var names []string
	for svcName, raw := range services {
		svc, ok := asMap(raw)
		if !ok {
			continue
		}
		if truthy(svc["build"]) {
			names = append(names, svcName)
		}
	}
	sort.Strings(names)
	return names
}

func grantSecret(services map[string]interface{}, svcName, name string) {
	svc, ok := asMap(services[svcName])
	if !ok {
		return
	}
	list, _ := asList(svc["secrets"])
	for _, entry := range list {
		if secretEntryName(entry) == name {
			return
		}
	}
	svc["secrets"] = append(list, name)
}

// isRelativeTo mirrors Python's Path.is_relative_to: path == base or under it.
func isRelativeTo(path, base string) bool {
	if path == base {
		return true
	}
	return strings.HasPrefix(path, base+string(filepath.Separator))
}
