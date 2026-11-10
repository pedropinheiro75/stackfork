package inventory

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
)

// fillServiceDetails reads a service's expandable settings straight off its
// entry in the already-isolated compose.json (design.md D3a: "no extra
// Docker calls"). Every field is best-effort: a shape this doesn't
// recognize is skipped, never an error, since a settings panel is a
// convenience, not a guard.
func fillServiceDetails(svc *Service, raw map[string]interface{}) {
	if image, ok := raw["image"].(string); ok {
		svc.Image = image
		name := imageName(image)
		svc.Kind = kindForImageName(name)
		svc.knownHTTPPorts = mailCatcherHTTPPorts[name]
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

// imageName reduces an image reference to the identity imageKind and
// mailCatcherHTTPPorts both key off: official image, "library/" prefix, any
// registry host, a tag or a digest all reduce to the same thing - the last
// path segment before the tag/digest, which correctly picks "minio" out of
// "minio/minio" and "postgres" out of "docker.io/library/postgres:17" or
// "postgres@sha256:...".
func imageName(image string) string {
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
	return name
}

// imageKind maps an image reference to one of D3a's known logo keys.
func imageKind(image string) string {
	return kindForImageName(imageName(image))
}

// ImageKind exposes imageKind for callers outside Collect's own pipeline
// (add-env-and-seed's clone-db --from-dump: which service is a
// postgres/mysql/mariadb database, read straight off compose.json's
// static image field, no live docker/Collect needed for that question).
func ImageKind(image string) string {
	return imageKind(image)
}

// mailCatcherHTTPPorts maps a mail catcher's image name to its known web UI
// port(s) (fix 3: "Mailpit's web UI port 8025 ... should be treated as HTTP
// by kind (known image -> known UI port)"). Keyed more specifically than
// the shared "mail" logo kind, since each mail catcher's UI port differs
// and its SMTP port must stay tcp; unknown images never match, by
// construction ("don't guess for unknown images").
var mailCatcherHTTPPorts = map[string][]int{
	"mailpit":  {8025},
	"smtp4dev": {80, 5000},
	"maildev":  {1080},
	"mailhog":  {8025},
}

func kindForImageName(name string) string {
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
	case "smtp4dev", "mailpit", "maildev", "mailhog":
		return "mail"
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

// defaultDBPort is the well-known container port each connection-string
// kind listens on, used to pick which of a service's (possibly several)
// endpoints is the one to build a connection string for.
var defaultDBPort = map[string]int{
	"postgres": 5432, "mysql": 3306, "mariadb": 3306, "redis": 6379, "mongo": 27017,
}

// connectionURL builds a copy-paste connection string for a known database
// kind (task 3.7, design.md D3a): the user and database name come from an
// explicit allowlist of non-secret identifier env keys read directly off
// the snapshot's environment map (never a password-shaped key, and never
// its value even to check it's set) - the password position is always the
// literal "<password>" placeholder. Empty when svc.Kind isn't one of the
// known kinds, or its default port's host address isn't known yet (svc.
// Endpoints not yet resolved, or the port genuinely unknown - a stopped
// legacy/--random-ports service, task 3.6).
func connectionURL(svc Service, raw map[string]interface{}) string {
	defaultPort, ok := defaultDBPort[svc.Kind]
	if !ok {
		return ""
	}
	addr, ok := endpointAddr(svc, defaultPort)
	if !ok {
		return ""
	}
	str := envLookup(raw)

	switch svc.Kind {
	case "postgres":
		user := str("POSTGRES_USER")
		if user == "" {
			user = "postgres"
		}
		db := str("POSTGRES_DB")
		if db == "" {
			db = user
		}
		return fmt.Sprintf("postgres://%s:<password>@%s/%s", user, addr, db)
	case "mysql", "mariadb":
		user := str("MYSQL_USER", "MARIADB_USER")
		if user == "" {
			user = "root"
		}
		uri := fmt.Sprintf("mysql://%s:<password>@%s", user, addr)
		if db := str("MYSQL_DATABASE", "MARIADB_DATABASE"); db != "" {
			uri += "/" + db
		}
		return uri
	case "mongo":
		user := str("MONGO_INITDB_ROOT_USERNAME")
		db := str("MONGO_INITDB_DATABASE")
		if user == "" {
			uri := "mongodb://" + addr
			if db != "" {
				uri += "/" + db
			}
			return uri
		}
		uri := fmt.Sprintf("mongodb://%s:<password>@%s", user, addr)
		if db != "" {
			uri += "/" + db
		}
		return uri
	case "redis":
		// A Redis that plainly has no password gets a URL without one: a
		// "<password>" there would make every client send AUTH and fail.
		if redisWithoutAuth(raw) {
			return "redis://" + addr
		}
		return fmt.Sprintf("redis://:<password>@%s", addr)
	default:
		return ""
	}
}

// endpointAddr finds the "host:port" address (as buildServiceEndpoints
// already rendered it for a tcp endpoint) for one of a service's known
// endpoints, or ok=false when that port isn't resolved yet (task 3.7's
// existing "stopped legacy/--random-ports service" case).
func endpointAddr(svc Service, port int) (string, bool) {
	for _, ep := range svc.Endpoints {
		if ep.Container == port && ep.Known {
			return ep.URL, true
		}
	}
	return "", false
}

// envLookup returns a closure reading the first set value among a list of
// env key names off a service's raw environment - the same allowlist
// pattern connectionURL and useCommands both need (never a password-shaped
// key). Goes through compose.NormalizeEnvironment, not a direct map
// assertion: with --no-interpolate a list-declared service's environment
// isn't normalized to a map by Compose itself (design.md D6).
func envLookup(raw map[string]interface{}) func(keys ...string) string {
	env := compose.NormalizeEnvironment(raw["environment"])
	return func(keys ...string) string {
		for _, k := range keys {
			if s, ok := env[k]; ok && s != "" {
				return s
			}
		}
		return ""
	}
}

// mailCatcherSMTPPorts maps a mail catcher's image name (imageName's
// identity, shared with mailCatcherHTTPPorts) to its SMTP port, for the
// "copy SMTP_HOST/SMTP_PORT" use-command (design.md D3b §3).
var mailCatcherSMTPPorts = map[string]int{
	"mailpit": 1025, "smtp4dev": 25, "maildev": 1025, "mailhog": 1025,
}

// UseCommand is one copy-ready command or value for a known service kind
// (task 3.10, design.md D3b §3): always placeholder-safe, like
// Service.ConnectionURL - never a real password.
type UseCommand struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// useCommands lists the extra copy actions D3b §3 wants beyond the plain
// connection URL: a client CLI invocation, an in-container `stackfork exec`
// form, or (for a mail catcher) the SMTP settings a host-run app would need.
// Every value is built the same placeholder-safe way ConnectionURL is.
func useCommands(svc Service, raw map[string]interface{}) []UseCommand {
	str := envLookup(raw)
	switch svc.Kind {
	case "postgres":
		if svc.ConnectionURL == "" {
			return nil
		}
		user := str("POSTGRES_USER")
		if user == "" {
			user = "postgres"
		}
		db := str("POSTGRES_DB")
		if db == "" {
			db = user
		}
		return []UseCommand{
			{Label: "psql", Value: fmt.Sprintf(`psql "%s"`, svc.ConnectionURL)},
			{Label: "stackfork exec", Value: fmt.Sprintf("stackfork exec %s psql -U %s %s", svc.Name, user, db)},
		}
	case "mysql", "mariadb":
		addr, ok := endpointAddr(svc, defaultDBPort[svc.Kind])
		if !ok {
			return nil
		}
		host, port := splitHostPort(addr)
		user := str("MYSQL_USER", "MARIADB_USER")
		if user == "" {
			user = "root"
		}
		db := str("MYSQL_DATABASE", "MARIADB_DATABASE")
		return []UseCommand{
			{Label: "mysql", Value: fmt.Sprintf("mysql -h %s -P %s -u %s -p<password> %s", host, port, user, db)},
			{Label: "stackfork exec", Value: fmt.Sprintf("stackfork exec %s mysql -u %s %s", svc.Name, user, db)},
		}
	case "redis":
		addr, ok := endpointAddr(svc, defaultDBPort["redis"])
		if !ok {
			return nil
		}
		host, port := splitHostPort(addr)
		return []UseCommand{
			{Label: "redis-cli", Value: fmt.Sprintf("redis-cli -h %s -p %s", host, port)},
		}
	case "mail":
		containerPort, ok := mailCatcherSMTPPorts[imageName(svc.Image)]
		if !ok {
			return nil
		}
		addr, ok := endpointAddr(svc, containerPort)
		if !ok {
			return nil
		}
		host, port := splitHostPort(addr)
		return []UseCommand{
			{Label: "SMTP_HOST", Value: host},
			{Label: "SMTP_PORT", Value: port},
		}
	default:
		return nil
	}
}

// splitHostPort splits a "host:port" address; best-effort, since it only
// ever runs on an address this package already built.
func splitHostPort(addr string) (host, port string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, ""
	}
	return host, port
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
// never a value). Goes through compose.NormalizeEnvironment, not a direct
// map assertion (design.md D6: a list-declared service's environment isn't
// normalized to a map with --no-interpolate).
func serviceEnvKeys(raw map[string]interface{}) []string {
	env := compose.NormalizeEnvironment(raw["environment"])
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
