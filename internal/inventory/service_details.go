package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// fillServiceDetails reads a service's expandable settings straight off its
// entry in the already-isolated compose.json (design.md D3a: "no extra
// Docker calls"). Every field is best-effort: a shape this doesn't
// recognize is skipped, never an error, since a settings panel is a
// convenience, not a guard.
func fillServiceDetails(svc *Service, raw map[string]interface{}) {
	if image, ok := raw["image"].(string); ok {
		svc.Image = image
		svc.Kind = imageKind(image)
	} else {
		svc.Kind = kindFromBuild(raw)
	}

	svc.Ports = servicePorts(raw)
	svc.Volumes = serviceVolumes(raw)
	svc.Healthcheck = serviceHealthcheck(raw)
	svc.DependsOn = serviceDependsOn(raw)
	svc.Replicas = serviceReplicas(raw)
	svc.EnvKeys = serviceEnvKeys(raw)
}

// imageKind maps an image reference to one of D3a's known logo keys, using
// only the image name (official image, "library/" prefix, any registry
// host, a tag or a digest all reduce to the same thing: the last path
// segment before the tag/digest - the same rule that correctly picks
// "minio" out of "minio/minio" and "postgres" out of
// "docker.io/library/postgres:17" or "postgres@sha256:...").
func imageKind(image string) string {
	ref := image
	if idx := strings.IndexByte(ref, '@'); idx >= 0 {
		ref = ref[:idx]
	}
	name := ref
	if idx := strings.LastIndexByte(ref, '/'); idx >= 0 {
		name = ref[idx+1:]
	}
	if idx := strings.IndexByte(name, ':'); idx >= 0 {
		name = name[:idx]
	}
	switch name {
	case "postgres", "postgresql":
		return "postgres"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	case "redis", "valkey":
		return "redis"
	case "mongo", "mongodb":
		return "mongo"
	case "neo4j":
		return "neo4j"
	case "nginx":
		return "nginx"
	case "caddy":
		return "caddy"
	case "node":
		return "node"
	case "bun":
		return "bun"
	case "python":
		return "python"
	case "golang", "go":
		return "go"
	case "rabbitmq":
		return "rabbitmq"
	case "minio":
		return "minio"
	case "gotenberg":
		return "gotenberg"
	case "smtp4dev":
		return "smtp4dev"
	case "mailpit":
		return "mailpit"
	case "maildev":
		return "maildev"
	default:
		return ""
	}
}

// kindFromBuild guesses a built image's logo from its Dockerfile's last
// FROM line (design.md D3a: "built images use the runtime of their
// Dockerfile base when known, else a neutral ... icon"). Best-effort: any
// missing file, unreadable path or unrecognized base falls back to "".
func kindFromBuild(raw map[string]interface{}) string {
	build, ok := raw["build"].(map[string]interface{})
	if !ok {
		return ""
	}
	context, _ := build["context"].(string)
	if context == "" {
		return ""
	}
	dockerfile, _ := build["dockerfile"].(string)
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	data, err := os.ReadFile(filepath.Join(context, dockerfile))
	if err != nil {
		return ""
	}
	var lastFrom string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "FROM") {
			lastFrom = fields[1]
		}
	}
	if lastFrom == "" {
		return ""
	}
	return imageKind(lastFrom)
}

// servicePorts reads the snapshot's ports list. "published" is a string
// once prepare has allocated a stable host port (state.PortAlloc's
// ApplyPortAllocations), but may be absent (no stable allocation: random-
// ports or a legacy instance) or a bare number straight from the source
// Compose file - all three are handled.
func servicePorts(raw map[string]interface{}) []PortMapping {
	list, ok := raw["ports"].([]interface{})
	if !ok {
		return nil
	}
	var out []PortMapping
	for _, item := range list {
		p, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		target, ok := asInt(p["target"])
		if !ok {
			continue
		}
		host, _ := asInt(p["published"])
		protocol, _ := p["protocol"].(string)
		out = append(out, PortMapping{Container: target, Host: host, Protocol: protocol})
	}
	return out
}

func serviceVolumes(raw map[string]interface{}) []VolumeMount {
	list, ok := raw["volumes"].([]interface{})
	if !ok {
		return nil
	}
	var out []VolumeMount
	for _, item := range list {
		v, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		source, _ := v["source"].(string)
		target, _ := v["target"].(string)
		if target == "" {
			continue
		}
		readOnly, _ := v["read_only"].(bool)
		out = append(out, VolumeMount{Name: source, Path: target, ReadOnly: readOnly})
	}
	return out
}

// serviceHealthcheck joins a `test` list (["CMD-SHELL", "..."] or
// ["CMD", "arg", ...]) into one display string, dropping the leading
// CMD/CMD-SHELL/NONE marker.
func serviceHealthcheck(raw map[string]interface{}) string {
	hc, ok := raw["healthcheck"].(map[string]interface{})
	if !ok {
		return ""
	}
	test, ok := hc["test"].([]interface{})
	if !ok || len(test) == 0 {
		return ""
	}
	parts := make([]string, 0, len(test))
	for _, t := range test {
		if s, ok := t.(string); ok {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	switch parts[0] {
	case "CMD", "CMD-SHELL", "NONE":
		parts = parts[1:]
	}
	return strings.Join(parts, " ")
}

// serviceDependsOn handles both JSON shapes Compose has used for
// `depends_on`: the long form (a map of service -> {condition, ...}) this
// installed Compose version emits, and the plain list form older ones did.
func serviceDependsOn(raw map[string]interface{}) []string {
	switch v := raw["depends_on"].(type) {
	case map[string]interface{}:
		names := make([]string, 0, len(v))
		for name := range v {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	case []interface{}:
		var names []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				names = append(names, s)
			}
		}
		return names
	default:
		return nil
	}
}

func serviceReplicas(raw map[string]interface{}) int {
	deploy, ok := raw["deploy"].(map[string]interface{})
	if !ok {
		return 0
	}
	n, ok := asInt(deploy["replicas"])
	if !ok || n <= 1 {
		return 0
	}
	return n
}

// serviceEnvKeys returns environment variable NAMES only (design.md D3a/D5:
// never a value); `environment` is a map once Compose has normalized it.
func serviceEnvKeys(raw map[string]interface{}) []string {
	env, ok := raw["environment"].(map[string]interface{})
	if !ok {
		return nil
	}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// asInt reads a JSON number that decoded as either json.Number (via
// compose.DecodeJSON's UseNumber) or a plain string (a stable host port
// port.ApplyPortAllocations wrote as text).
func asInt(v interface{}) (int, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	case string:
		n, err := strconv.Atoi(t)
		if err != nil {
			return 0, false
		}
		return n, true
	case float64:
		return int(t), true
	default:
		return 0, false
	}
}
