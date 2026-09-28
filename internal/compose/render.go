package compose

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// DockerEnv mirrors Python's docker_env(): never inherit a different
// project's Compose selection or implicit .env.
func DockerEnv() []string {
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
	return env
}

// renderEnv builds the minimal, explicit environment Render's `docker
// compose config` subprocess runs with (design.md D6, security fix): only
// what Docker/Compose need to function (PATH, a home directory, a temp
// directory, anything DOCKER_*-prefixed), never the calling process's whole
// ambient environment - see Render's doc comment for why. Named after, but
// deliberately not built from, DockerEnv.
func renderEnv(hostname string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		switch {
		case key == "PATH", key == "HOME", key == "USERPROFILE",
			key == "TMPDIR", key == "TEMP", key == "TMP":
			env = append(env, kv)
		case strings.HasPrefix(key, "DOCKER_"):
			env = append(env, kv)
		}
	}
	env = append(env, "COMPOSE_DISABLE_ENV_FILE=1")
	if hostname != "" {
		// RR_DEV_HOSTNAME is deprecated: kept only so adapters written
		// before the sf rename keep working (design.md D1/D2).
		env = append(env, "DEV_HOSTNAME="+hostname, "RR_DEV_HOSTNAME="+hostname)
	}
	return env
}

// Render mirrors Python's render(root, files, env_file, hostname): shells
// out to `docker compose config --no-env-resolution --format json` and
// suppresses Compose's own error output (it can contain interpolated
// secrets).
//
// Security fix (design.md D6, verified 2026-09-28 on Compose v5.1.2): the
// subprocess's environment is NOT the calling process's ambient environment
// (unlike DockerEnv(), used by up/run/etc where that's the whole point -
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
func Render(root string, files []string, envFile string, hostname string) (interface{}, error) {
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

	cmd := exec.Command("docker", args...)
	cmd.Env = renderEnv(hostname)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Compose config failed; check file syntax, required variables and paths locally. Output suppressed to avoid exposing secrets.")
	}
	return DecodeJSON(stdout.Bytes())
}
