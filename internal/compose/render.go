package compose

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// DockerEnv mirrors Python's docker_env(): never inherit a different
// project's Compose selection or implicit .env. values maps a secret's
// manifest name to its value; nil for a child that needs no secret.
func DockerEnv(values map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if strings.HasPrefix(key, "COMPOSE_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "COMPOSE_DISABLE_ENV_FILE=1")
	// Secret values go only to this child (design D9), under every alias of
	// the name; they are never set in stackfork's own environment.
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, alias := range SecretEnvAliases(n) {
			if ReservedSecretName(alias) { // a manifest from before the name check could hold one
				continue
			}
			env = append(env, alias+"="+values[n])
		}
	}
	return env
}

// windowsRenderEnv is what Windows programs need beyond the list in
// renderEnv: SystemRoot (Winsock), the program-data and app-data directories
// (docker CLI plugins and config) and PATHEXT. None holds a secret.
var windowsRenderEnv = map[string]bool{
	"SYSTEMROOT": true, "WINDIR": true, "SYSTEMDRIVE": true, "PATHEXT": true, "COMSPEC": true,
	"PROGRAMDATA": true, "PROGRAMFILES": true, "PROGRAMW6432": true, "PROGRAMFILES(X86)": true,
	"APPDATA": true, "LOCALAPPDATA": true, "HOMEDRIVE": true, "HOMEPATH": true,
}

// renderEnv builds the minimal, explicit environment Render's `docker
// compose config` subprocess runs with (design.md D6, security fix): only
// what Docker/Compose need to function (PATH, a home directory, a temp
// directory, anything DOCKER_*-prefixed), never the calling process's whole
// ambient environment - see Render's doc comment for why. Named after, but
// deliberately not built from, DockerEnv. sentinels (from secretSentinels)
// are added last so a named secret's placeholder always wins over anything
// same-named in the allowlist above (never happens in practice, but wins
// deterministically either way).
func renderEnv(hostname string, sentinels map[string]string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if runtime.GOOS == "windows" {
			// Windows names are case-insensitive (Path), and the docker CLI
			// needs the system directories to start and to find its plugins.
			key = strings.ToUpper(key)
		}
		switch {
		case key == "PATH", key == "HOME", key == "USERPROFILE",
			key == "TMPDIR", key == "TEMP", key == "TMP":
			env = append(env, kv)
		case strings.HasPrefix(key, "DOCKER_"):
			env = append(env, kv)
		case runtime.GOOS == "windows" && windowsRenderEnv[key]:
			env = append(env, kv)
		}
	}
	env = append(env, "COMPOSE_DISABLE_ENV_FILE=1")
	if hostname != "" {
		// RR_DEV_HOSTNAME is deprecated: kept only so adapters written
		// before the sf rename keep working (design.md D1/D2).
		env = append(env, "DEV_HOSTNAME="+hostname, "RR_DEV_HOSTNAME="+hostname)
	}
	for name, sentinel := range sentinels {
		env = append(env, name+"="+sentinel)
	}
	return env
}

// SecretEnvAliases returns the env var name(s) a named secret's real value
// must be set under, so a service's own `environment:` can reference it
// directly as `${NAME}` with no Compose `secrets:` block involved
// (design.md D6): the secret's own manifest name always, plus - for
// npm_token specifically, the one built-in secret name (the --npmrc sugar)
// - its conventional uppercase alias NPM_TOKEN, since real adapters read
// npm's own convention (`_authToken=${NPM_TOKEN}`), not stackfork's
// internal lowercase name. Any other secret is entirely user-named via
// `--secret NAME=...`, and NAME is exactly what the adapter is expected to
// reference - no aliasing needed. The single source of truth for this
// mapping: used both here (secretSentinels, so the reference survives
// render literally) and by lifecycle.Execute (so it resolves for real at
// up/run).
func SecretEnvAliases(name string) []string {
	if name == "npm_token" {
		return []string{name, "NPM_TOKEN"}
	}
	return []string{name}
}

// secretSentinels returns, for each of prepare's named secrets (and its
// SecretEnvAliases), the env var names Render must protect from resolving
// to empty, each mapped to a unique sentinel value: Render must neither
// leave a direct `${NAME}` reference empty (breaks the adapter, this was
// 253fb81's regression) nor resolve the real value (the leak this whole fix
// is for) - so it resolves to a harmless placeholder instead, swapped back
// to the literal "${NAME}" reference afterwards.
//
// foldCase is true on Windows, where names are case-insensitive: npm_token
// and its alias NPM_TOKEN are one variable there, and the child keeps only
// the last of the two (os/exec), so a random map order decided which
// reference the snapshot got back. Names that differ only by case share one
// sentinel; the spelling that sorts first (NPM_TOKEN) is the one restored,
// and Windows resolves it for either spelling at up.
func secretSentinels(secretNames []string, foldCase bool) map[string]string {
	var aliases []string
	for _, name := range secretNames {
		aliases = append(aliases, SecretEnvAliases(name)...)
	}
	sort.Strings(aliases)
	out := map[string]string{}
	seen := map[string]bool{}
	for _, alias := range aliases {
		key := alias
		if foldCase {
			key = strings.ToUpper(alias)
		}
		if !seen[key] {
			seen[key] = true
			out[alias] = "__stackfork_ref_" + alias + "__"
		}
	}
	return out
}

// unresolvedVarPattern matches Compose's own "variable is not set" warning,
// which names the variable and never its value (there isn't one - it
// resolved to nothing). Render scans its subprocess's stderr for this on
// every successful render, never on a failed one (that path already
// suppresses stderr entirely).
var unresolvedVarPattern = regexp.MustCompile(`\\?"([A-Za-z_][A-Za-z0-9_]*)\\?" variable is not set`)

// unresolvedVarNames extracts and dedupes every variable name Compose
// warned about from Render's own stderr (design.md D6): a bare `${VAR}`
// that resolved to nothing, never a `${VAR:-default}` (Compose doesn't warn
// for those) and never one of secretSentinels' protected names (those
// always resolve to their sentinel, never "not set").
func unresolvedVarNames(stderrText string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range unresolvedVarPattern.FindAllStringSubmatch(stderrText, -1) {
		if name := m[1]; !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Render mirrors Python's render(root, files, env_file, hostname): shells
// out to `docker compose config --no-env-resolution --format json` and
// suppresses Compose's own error output (it can contain interpolated
// secrets).
//
// Security fix (design.md D6, verified 2026-09-28 on Compose v5.1.2): the
// subprocess's environment is NOT the calling process's ambient environment
// (unlike DockerEnv(nil), used by up/run/etc where that's the whole point -
// see its doc comment). It is a minimal, explicit allowlist instead
// (renderEnv): only what Docker/Compose need to function at all (PATH,
// HOME/USERPROFILE, TMPDIR-family, anything DOCKER_*), plus DEV_HOSTNAME/
// RR_DEV_HOSTNAME. Compose's ${VAR} interpolation looks a name up in the
// subprocess's own environment first, then the explicit --env-file - so
// whatever the *caller's* shell happens to have set (NPM_TOKEN, a database
// password, ...) is invisible to it and can never resolve into the
// snapshot; reproduced before this fix with a canary
// (`CANARY_T=leaked123` + `POSTGRES_PASSWORD: ${CANARY_T}` in the source
// file -> "leaked123" baked into compose.json). An unresolved reference
// becomes an empty string (Compose's normal "variable is not set,
// defaulting to a blank string" behaviour, same as any unset var), which
// LiteralPassword already treats as non-literal.
//
// This was fixed once already with `docker compose config --no-interpolate`
// (killing ALL interpolation, not just the ambient-environment path) and
// reverted: --no-interpolate is all-or-nothing, and the template adapters
// (assets/nuxt/compose.yaml, assets/go/compose.yaml) depend on real
// interpolation of their *own* --env-file-supplied variables for
// build.context, volumes, deploy.replicas and env_file (`${DEV_CHECKOUT:?}`
// and friends) - TestLiveTemplate caught this the same day, refusing on an
// unresolved env_file entry. Scoping the environment instead of disabling
// interpolation keeps that working while still closing the leak: Compose's
// own interpolation engine still runs, it just never sees anything the
// caller didn't explicitly hand it via --env-file or the hostname
// argument.
//
// Named-secret regression (253fb81, fixed here): a service can reference a
// named secret directly, `environment: { NPM_TOKEN: ${NPM_TOKEN} }`, with no
// Compose `secrets:` block at all - the *scoped* environment above still
// left that empty (never in the allowlist, never in --env-file, only ever
// resolved by lifecycle.Execute at up/run time), and an empty string baked
// into the snapshot has no "${...}" left for up's own interpolation pass to
// find. secretNames (prepare's resolved --secret/--npmrc names) get a
// sentinel value each (secretSentinels) instead of nothing, so Compose
// resolves the reference to something harmless rather than leaving it "not
// set" or supplying the real value; the sentinel is then swapped back to
// the literal "${NAME}" text in the rendered JSON, restoring the reference
// for `docker compose -f compose.json up`/`run` to interpolate for real
// (verified empirically: a snapshot's literal "${NAME}" re-interpolates
// correctly against a later invocation's own process environment, exactly
// like any other Compose file).
//
// The second return value names every OTHER `${VAR}` Compose could not
// resolve at all (not in the scoped environment, not in --env-file, no
// secret sentinel, and no `:-default` in the source) - `prepare` surfaces
// these as a one-line warning rather than the silent empty that broke an
// adapter reading NPM_TOKEN this way in the first place.
func Render(root string, files []string, envFile string, hostname string, secretNames []string) (interface{}, []string, error) {
	ef := os.DevNull
	if envFile != "" {
		ef = envFile
	}
	args := []string{"compose", "-p", identity.RenderProject, "--project-directory", root,
		"--env-file", ef, "--profile", "*"}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	args = append(args, "config", "--no-env-resolution", "--format", "json")

	sentinels := secretSentinels(secretNames, runtime.GOOS == "windows")
	cmd := exec.Command("docker", args...)
	cmd.Env = renderEnv(hostname, sentinels)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, errors.New("Compose config failed; check file syntax, required variables and paths locally. Output suppressed to avoid exposing secrets.")
	}

	out := stdout.Bytes()
	for name, sentinel := range sentinels {
		out = bytes.ReplaceAll(out, []byte(sentinel), []byte("${"+name+"}"))
	}

	model, err := DecodeJSON(out)
	if err != nil {
		return nil, nil, err
	}
	return model, unresolvedVarNames(stderr.String()), nil
}

// SourceSecrets are the top-level secrets of the Compose source that a
// service (or its build) uses and that have no source of their own: no
// `file:`, `content:` or `external`. The value is the env var an
// `environment:` source reads, or "". Best effort: nil when Compose cannot
// render the files (prepare reports that).
func SourceSecrets(root string, files []string, envFile string) map[string]string {
	if len(files) == 0 {
		return nil
	}
	model, _, err := Render(root, files, envFile, "", nil)
	if err != nil {
		return nil
	}
	m, _ := asMap(model)
	top, _ := asMap(m["secrets"])
	services, _ := asMap(m["services"])
	used := map[string]bool{}
	for _, raw := range services {
		svc, _ := asMap(raw)
		lists := [][]interface{}{}
		l, _ := asList(svc["secrets"])
		lists = append(lists, l)
		if b, ok := asMap(svc["build"]); ok {
			l, _ := asList(b["secrets"])
			lists = append(lists, l)
		}
		for _, list := range lists {
			for _, e := range list {
				used[secretEntryName(e)] = true
			}
		}
	}
	out := map[string]string{}
	for name := range used {
		def, _ := asMap(top[name])
		if _, ok := top[name]; !ok || def["file"] != nil || def["content"] != nil || truthy(def["external"]) {
			continue
		}
		env, _ := def["environment"].(string)
		out[name] = env
	}
	return out
}
