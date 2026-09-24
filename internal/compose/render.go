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

// Render mirrors Python's render(root, files, env_file, hostname): shells
// out to `docker compose config --no-env-resolution --format json` and
// suppresses Compose's own error output (it can contain interpolated secrets).
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

	env := DockerEnv()
	if hostname != "" {
		env = append(env, "DEV_HOSTNAME="+hostname, "RR_DEV_HOSTNAME="+hostname)
	}

	cmd := exec.Command("docker", args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Compose config failed; check file syntax, required variables and paths locally. Output suppressed to avoid exposing secrets.")
	}
	return DecodeJSON(stdout.Bytes())
}
