package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// Isolate mirrors Python's isolate(source, root, project, env_file) exactly,
// including error messages. source is JSON-shaped (map[string]interface{}
// etc, as produced by DecodeJSON); root need not be pre-resolved. envFile
// is "" for Python's None.
func Isolate(source interface{}, root, project, envFile string) (interface{}, error) {
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
			if truthy(definition["external"]) {
				return nil, fmt.Errorf("%s.%s: external resource requires a project-specific replacement", kind, name)
			}
			driver, _ := definition["driver"].(string)
			driverOK := definition["driver"] == nil || driver == "local" || driver == "bridge"
			if truthy(definition["driver_opts"]) || !driverOK {
				return nil, fmt.Errorf("%s.%s: custom driver/storage needs review", kind, name)
			}
			if kind == "networks" {
				if ipam, ok := asMap(definition["ipam"]); ok && truthy(ipam["config"]) {
					return nil, fmt.Errorf("networks.%s: fixed subnets must be removed for concurrent stacks", name)
				}
			}
			if kind == "volumes" || kind == "networks" {
				definition["name"] = fmt.Sprintf("%s_%s", project, name)
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
				return nil, fmt.Errorf("%s.%s: cannot isolate automatically", name, key)
			}
		}
		for _, key := range []string{"network_mode", "pid", "ipc"} {
			v := service[key]
			if v == nil {
				continue
			}
			s, isString := v.(string)
			if !isString || (s != "none" && s != "private") {
				return nil, fmt.Errorf("%s.%s: shared namespace requires a project-specific replacement", name, key)
			}
		}
		delete(service, "container_name")

		if truthy(service["build"]) {
			service["image"] = fmt.Sprintf("%s-%s:dev", project, name)
			if build, ok := asMap(service["build"]); ok {
				delete(build, "tags")
			}
		}

		if ports, ok := asList(service["ports"]); ok {
			for _, raw := range ports {
				port, ok := asMap(raw)
				if !ok || !isJSONInt(port["target"]) {
					return nil, fmt.Errorf("%s: unresolved port mapping", name)
				}
				delete(port, "published")
				port["host_ip"] = "127.0.0.1"
				port["mode"] = "ingress"
			}
		}

		if entries, ok := asList(service["env_file"]); ok {
			for _, raw := range entries {
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
					return nil, fmt.Errorf("%s: env_file must reference only the explicitly supplied --env-file; override the original env_file list", name)
				}
			}
		}

		if mounts, ok := asList(service["volumes"]); ok {
			for _, raw := range mounts {
				mount, ok := asMap(raw)
				if !ok {
					continue
				}
				if t, _ := mount["type"].(string); t != "bind" {
					continue
				}
				source, _ := mount["source"].(string)
				path, err := identity.ResolvePath(source)
				if err != nil {
					return nil, err
				}
				if strings.HasSuffix(filepath.Base(path), ".sock") || path == "/proc" || path == "/sys" || path == "/dev" {
					return nil, fmt.Errorf("%s: host control mount is not isolated", name)
				}
				if _, err := os.Stat(path); err != nil {
					return nil, fmt.Errorf("%s: bind source does not exist: %s", name, path)
				}
				if !truthy(mount["read_only"]) && !isRelativeTo(path, rootResolved) {
					return nil, fmt.Errorf("%s: writable bind outside checkout: %s", name, path)
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

// isRelativeTo mirrors Python's Path.is_relative_to: path == base or under it.
func isRelativeTo(path, base string) bool {
	if path == base {
		return true
	}
	return strings.HasPrefix(path, base+string(filepath.Separator))
}
