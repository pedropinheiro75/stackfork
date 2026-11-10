package inventory

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/pedropinheiro75/stackfork/internal/compose"
)

// LiteralPassword resolves a password ONLY when it is written literally in
// one of the instance's source Compose files, under a known local-database
// key or a Redis --requirepass argument (design.md D3b's passwords rule,
// task 3.11). This is the one deliberate exception to "the inventory never
// contains a secret value" - it is NEVER called from Collect or anything
// Collect calls, only from the dashboard's token-protected POST /api/copy.
//
// Values from `--secret`, a Compose `env_file:` entry, or `${VAR}`
// interpolation are refused (returns ok=false), never guessed at:
//   - `${VAR}` interpolation survives literally in compose.json because
//     prepare always renders with `--no-interpolate` (design.md D6's
//     security fix - `--no-env-resolution` alone only skips env_file
//     merging, it does NOT stop interpolation, which would otherwise
//     resolve from whatever happens to be set in prepare's own
//     environment and get baked into the snapshot as a real secret), so a
//     value containing "${" is never treated as literal.
//   - A service that declares any `env_file:` at all has this whole
//     lookup refused for it (ponytail: conservative rather than parsing
//     which specific keys came from the file - Go stdlib has no YAML
//     parser to draw that line precisely; the upgrade path, if ever
//     needed, is a real Compose-aware source parse).
//   - `--secret`-backed values only ever reach a service's environment
//     through `${VAR}` interpolation in this tool's own design (D6), which
//     the interpolation check above already catches.
func LiteralPassword(files []string, service, kind string, composeServiceRaw map[string]interface{}) (string, bool) {
	keys, ok := passwordKeysByKind[kind]
	if !ok {
		return "", false
	}
	usesEnvFile, verified := serviceUsesEnvFile(files, service)
	if !verified || usesEnvFile {
		// Fail closed: no source files to check, or one couldn't be read,
		// is exactly as unverifiable as a real env_file - refuse rather
		// than assume clean.
		return "", false
	}

	// Goes through compose.NormalizeEnvironment, not a direct map
	// assertion: with --no-interpolate a list-declared service's
	// environment isn't normalized to a map by Compose itself. A missing
	// key here (map or list, either way) correctly falls through to the
	// requirepass check / refusal below, same as before.
	env := compose.NormalizeEnvironment(composeServiceRaw["environment"])
	for _, key := range keys {
		if s, ok := env[key]; ok && isLiteralValue(s) {
			return s, true
		}
	}
	if kind == "redis" {
		if pw, ok := requirepassFromCommand(composeServiceRaw); ok {
			return pw, true
		}
	}
	return "", false
}

// CopyValue resolves one D3b "copy" value for the dashboard's
// POST /api/copy (task 3.11): the same placeholder-bearing string already
// shown on the page (a service's ConnectionURL, its primary UseCommand, or
// the stack's EnvBlock), with the real password substituted wherever
// LiteralPassword can verify it - the placeholder stays otherwise. This is
// the only caller of LiteralPassword; svcRaw is an instance's compose.json
// "services" map (name -> raw entry, e.g. from ReadComposeServices).
func CopyValue(kind, service string, services []Service, files []string, svcRaw map[string]interface{}) (string, bool) {
	switch kind {
	case "database_url":
		svc := findService(services, service)
		if svc == nil {
			return "", false
		}
		return withRealPassword(svc.ConnectionURL, svc.Kind, svc.Name, files, svcRaw), true
	case "cli":
		svc := findService(services, service)
		if svc == nil {
			return "", false
		}
		return withRealPassword(primaryUseCommand(svc.UseCommands), svc.Kind, svc.Name, files, svcRaw), true
	case "env_block":
		block, err := EnvBlockWithPasswords(services, files, svcRaw)
		return block, err == nil
	default:
		return "", false
	}
}

func findService(services []Service, name string) *Service {
	for i := range services {
		if services[i].Name == name {
			return &services[i]
		}
	}
	return nil
}

// primaryUseCommand picks the one UseCommand a "cli" copy means: the first
// that carries the password placeholder (psql/mysql/redis-cli), or - for a
// kind with none, e.g. mail's SMTP_HOST/SMTP_PORT - the first command as-is.
func primaryUseCommand(cmds []UseCommand) string {
	for _, c := range cmds {
		if strings.Contains(c.Value, "<password>") {
			return c.Value
		}
	}
	if len(cmds) > 0 {
		return cmds[0].Value
	}
	return ""
}

// withRealPassword substitutes svc's real password into a placeholder
// string already containing "<password>", only when LiteralPassword can
// verify one; any other case (no placeholder present, no raw entry, no
// literal value) returns the placeholder unchanged.
func withRealPassword(placeholder, kind, service string, files []string, svcRaw map[string]interface{}) string {
	if placeholder == "" || !strings.Contains(placeholder, "<password>") {
		return placeholder
	}
	raw, ok := svcRaw[service].(map[string]interface{})
	if !ok {
		return placeholder
	}
	pw, ok := LiteralPassword(files, service, kind, raw)
	if !ok {
		return placeholder
	}
	return strings.ReplaceAll(placeholder, "<password>", pw)
}

// envBlockWithRealPasswords rebuilds buildEnvBlock's exact placeholder
// output (single source of truth for its line order/shape) and substitutes
// each DATABASE_URL/REDIS_URL/MONGODB_URL line's password with the real
// value from the specific service that line came from, when resolvable.
//
// A literal password with a control character (a line break would add a line,
// so a variable, to the block) is an error that names the variable, never the
// value.
func EnvBlockWithPasswords(services []Service, files []string, svcRaw map[string]interface{}) (string, error) {
	placeholder := buildEnvBlock(services)
	if placeholder == "" {
		return "", nil
	}
	firstByKey := map[string]Service{}
	for _, svc := range services {
		var key string
		switch svc.Kind {
		case "postgres", "mysql", "mariadb":
			key = "DATABASE_URL"
		case "redis":
			key = "REDIS_URL"
		case "mongo":
			key = "MONGODB_URL"
		default:
			continue
		}
		if _, ok := firstByKey[key]; !ok {
			firstByKey[key] = svc
		}
	}
	lines := strings.Split(placeholder, "\n")
	for i, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		svc, ok := firstByKey[key]
		if !ok {
			continue
		}
		real := withRealPassword(value, svc.Kind, svc.Name, files, svcRaw)
		if strings.ContainsFunc(real, unicode.IsControl) {
			return "", fmt.Errorf("%s: the password of service %s has a control character; stackfork will not put it in an environment block", key, svc.Name)
		}
		lines[i] = key + "=" + real
	}
	return strings.Join(lines, "\n"), nil
}

// passwordKeysByKind is D3b's exact list of "known local-dev password
// keys", one set per database kind.
var passwordKeysByKind = map[string][]string{
	"postgres": {"POSTGRES_PASSWORD"},
	"mysql":    {"MYSQL_PASSWORD", "MYSQL_ROOT_PASSWORD"},
	"mariadb":  {"MARIADB_PASSWORD", "MARIADB_ROOT_PASSWORD"},
	"mongo":    {"MONGO_INITDB_ROOT_PASSWORD"},
	"redis":    {"REDIS_PASSWORD"},
}

// isLiteralValue rejects anything empty or carrying unresolved Compose
// interpolation syntax.
func isLiteralValue(s string) bool {
	return s != "" && !strings.Contains(s, "${")
}

var requirepassPattern = regexp.MustCompile(`--requirepass[= ]("[^"]*"|'[^']*'|\S+)`)

// redisWithoutAuth reports whether a Redis service is known to have no
// password. It is a positive check, never a guess: the official redis or
// valkey image, no entrypoint, env_file, configs or password variable, no
// mount other than the /data directory, and either no command or
// `redis-server`/`valkey-server` with only flags that cannot set a
// password. Anything else (an inline ACL, a config file, a shell, a custom
// image) keeps the "<password>" placeholder, so a URL never drops a
// credential the server needs.
func redisWithoutAuth(raw map[string]interface{}) bool {
	for k, v := range raw {
		// `docker compose config` writes unset keys as null (entrypoint: null).
		if v != nil && !redisSafeKeys[k] {
			return false // build, entrypoint, env_file, configs, secrets, post_start, ... or a new key
		}
	}
	if !officialRedisImage(fmt.Sprint(raw["image"])) {
		return false
	}
	// Any variable can change what runs (PATH, LD_PRELOAD, REDIS_PASSWORD...).
	if len(compose.NormalizeEnvironment(raw["environment"])) > 0 {
		return false
	}
	if hc, ok := raw["healthcheck"].(map[string]interface{}); ok && !pingHealthcheck(hc) {
		return false // a healthcheck runs a command: CONFIG SET requirepass would set a password
	}
	if vols, ok := raw["volumes"].([]interface{}); ok {
		for _, v := range vols {
			if volumeTarget(v) != "/data" {
				return false
			}
		}
	} else if raw["volumes"] != nil {
		return false
	}
	return harmlessRedisCommand(raw["command"])
}

func officialRedisImage(image string) bool {
	repo := image
	if i := strings.Index(repo, "@"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "docker.io/"), "library/")
	return repo == "redis" || repo == "valkey/valkey"
}

// volumeTarget is the container path of a Compose volume entry, in its
// short ("src:dst[:mode]") or long ({target: dst}) form.
func volumeTarget(v interface{}) string {
	switch m := v.(type) {
	case string:
		parts := strings.Split(m, ":")
		if len(parts) >= 2 {
			return strings.TrimRight(parts[1], "/")
		}
		return strings.TrimRight(parts[0], "/")
	case map[string]interface{}:
		return strings.TrimRight(fmt.Sprint(m["target"]), "/")
	}
	return ""
}

// redisSafeKeys are the Compose service keys that cannot set a password or
// run anything at start. Every other key (build, entrypoint, env_file,
// configs, secrets, post_start, pre_stop, user, ...) keeps the placeholder.
var redisSafeKeys = map[string]bool{
	"image": true, "command": true, "environment": true, "volumes": true, "ports": true, "expose": true,
	"networks": true, "restart": true, "labels": true, "healthcheck": true, "container_name": true,
	"hostname": true, "depends_on": true, "stop_grace_period": true, "stop_signal": true,
	"pull_policy": true, "platform": true,
}

// pingHealthcheck reports whether a healthcheck only pings: its test is
// `redis-cli ping` (or valkey-cli), as CMD or CMD-SHELL, or it is disabled.
func pingHealthcheck(hc map[string]interface{}) bool {
	if d, _ := hc["disable"].(bool); d {
		return true
	}
	var parts []string
	switch t := hc["test"].(type) {
	case nil:
		return true
	case string:
		parts = strings.Fields(t)
	case []interface{}:
		for _, p := range t {
			parts = append(parts, strings.Fields(fmt.Sprint(p))...)
		}
	default:
		return false
	}
	switch strings.Join(parts, " ") {
	case "NONE", "CMD redis-cli ping", "CMD-SHELL redis-cli ping", "CMD valkey-cli ping", "CMD-SHELL valkey-cli ping", "redis-cli ping", "valkey-cli ping":
		return true
	}
	return false
}

// redisSafeFlags cannot set a password, a user, an ACL or a config file.
var redisSafeFlags = map[string]bool{
	"--appendonly": true, "--appendfsync": true, "--save": true, "--port": true, "--loglevel": true,
	"--databases": true, "--maxmemory": true, "--maxmemory-policy": true, "--protected-mode": true,
	"--bind": true, "--dir": true, "--dbfilename": true, "--io-threads": true, "--tcp-keepalive": true,
	"--timeout": true, "--notify-keyspace-events": true,
}

func harmlessRedisCommand(cmd interface{}) bool {
	var args []string
	switch c := cmd.(type) {
	case nil:
		return true
	case string:
		args = strings.Fields(c)
	case []interface{}:
		for _, a := range c {
			args = append(args, fmt.Sprint(a))
		}
	default:
		return false
	}
	if len(args) == 0 {
		return true
	}
	if args[0] != "redis-server" && args[0] != "valkey-server" {
		return false
	}
	flag := false
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "--") {
			if !redisSafeFlags[strings.ToLower(a)] {
				return false
			}
			flag = true
		} else if !flag {
			return false // a config file path before any flag
		}
	}
	return true
}

// requirepassFromCommand reads a Redis service's own `--requirepass`
// command-line argument, the official redis image's way of setting a
// password (unlike the env-var pattern bitnami-style images use).
func requirepassFromCommand(raw map[string]interface{}) (string, bool) {
	var parts []string
	switch cmd := raw["command"].(type) {
	case []interface{}:
		for _, c := range cmd {
			if s, ok := c.(string); ok {
				parts = append(parts, s)
			}
		}
	case string:
		parts = []string{cmd}
	default:
		return "", false
	}
	joined := strings.Join(parts, " ")
	m := requirepassPattern.FindStringSubmatch(joined)
	if m == nil {
		return "", false
	}
	value := strings.Trim(m[1], `"'`)
	if !isLiteralValue(value) {
		return "", false
	}
	return value, true
}

// serviceBlockPattern finds one top-level service's YAML block: from its
// "  <name>:" line (exactly two spaces of indent, the standard depth for a
// service under "services:") to the next line at two spaces of indent or
// less (the next service, or the end of the services: map).
func serviceBlockPattern(service string) *regexp.Regexp {
	return regexp.MustCompile(`(?ms)^  ` + regexp.QuoteMeta(service) + `:\s*\n(.*?)(?:\n  \S|\z)`)
}

// serviceUsesEnvFile is a conservative, line-based scan of the source
// Compose files for whether the named service declares an `env_file:`
// directive - stdlib only, not a full YAML parser (see LiteralPassword's
// doc comment), so it only needs to answer this one yes/no question
// reliably enough to refuse rather than risk it. verified is false (fail
// closed, caller must refuse) when there are no files to check or any one
// of them can't be read - "can't prove it's clean" is treated the same as
// "it's dirty", never silently skipped.
func serviceUsesEnvFile(files []string, service string) (usesEnvFile, verified bool) {
	if len(files) == 0 {
		return false, false
	}
	pattern := serviceBlockPattern(service)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, false
		}
		m := pattern.FindSubmatch(data)
		if m == nil {
			continue
		}
		for _, line := range strings.Split(string(m[1]), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "env_file") {
				return true, true
			}
		}
	}
	return false, true
}
