package learn

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// dbEngines is checked against an image's repository (before ':'), longest
// prefix first so "mariadb" doesn't get caught by a hypothetical "maria"
// entry etc. Order here also breaks ties for images that could match more
// than one (there are none today).
var dbEngines = []string{"postgres", "postgis", "mysql", "mariadb", "mongo", "redis"}

var majorVersion = regexp.MustCompile(`^v?(\d+)`)

func inferDB(image string) (engine, major string) {
	if image == "" {
		return "", ""
	}
	repo, tag, hasTag := strings.Cut(image, ":")
	// Strip a registry/namespace prefix (e.g. "docker.io/library/postgres").
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		repo = repo[idx+1:]
	}
	for _, engine := range dbEngines {
		if repo == engine {
			if hasTag {
				if m := majorVersion.FindStringSubmatch(tag); m != nil {
					major = m[1]
				}
			}
			return engine, major
		}
	}
	return "", ""
}

// servicesFromRendered extracts Facts.Services from a rendered (not yet
// isolated) `docker compose config` JSON model, so ports/volumes/images
// reflect what the project actually declares.
func servicesFromRendered(rendered interface{}) []knowledge.Service {
	model, ok := rendered.(map[string]interface{})
	if !ok {
		return nil
	}
	svcMap, ok := model["services"].(map[string]interface{})
	if !ok {
		return nil
	}
	names := make([]string, 0, len(svcMap))
	for name := range svcMap {
		names = append(names, name)
	}
	sort.Strings(names)

	var services []knowledge.Service
	for _, name := range names {
		raw, ok := svcMap[name].(map[string]interface{})
		if !ok {
			continue
		}
		image, _ := raw["image"].(string)
		engine, major := inferDB(image)
		svc := knowledge.Service{
			Name:           name,
			Image:          image,
			Build:          raw["build"] != nil,
			DBEngine:       engine,
			DBMajorVersion: major,
			Ports:          servicePorts(raw["ports"]),
			Volumes:        serviceVolumes(raw["volumes"]),
			Networks:       serviceNetworks(raw["networks"]),
			Healthcheck:    raw["healthcheck"] != nil,
			Profiles:       stringSlice(raw["profiles"]),
		}
		services = append(services, svc)
	}
	return services
}

func servicePorts(raw interface{}) []string {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var ports []string
	for _, entry := range list {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		target := jsonNumberString(m["target"])
		if target == "" {
			continue
		}
		if published := jsonNumberString(m["published"]); published != "" {
			ports = append(ports, published+":"+target)
		} else {
			ports = append(ports, target)
		}
	}
	return ports
}

func serviceVolumes(raw interface{}) []string {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var volumes []string
	for _, entry := range list {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		kind, _ := m["type"].(string)
		source, _ := m["source"].(string)
		if source == "" {
			continue
		}
		if kind == "bind" {
			volumes = append(volumes, "bind:"+source)
		} else {
			volumes = append(volumes, source)
		}
	}
	return volumes
}

func serviceNetworks(raw interface{}) []string {
	switch v := raw.(type) {
	case map[string]interface{}:
		names := make([]string, 0, len(v))
		for name := range v {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	case []interface{}:
		return stringSlice(v)
	default:
		return nil
	}
}

func stringSlice(raw interface{}) []string {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func jsonNumberString(v interface{}) string {
	switch n := v.(type) {
	case json.Number:
		return n.String()
	case string:
		return n
	default:
		return ""
	}
}
