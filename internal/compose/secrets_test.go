package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Task 6.1: ParseSecretFlag validates shape only, never resolves a value.
func TestParseSecretFlag(t *testing.T) {
	name, ref, err := ParseSecretFlag("npm_token=env:MY_TOKEN")
	if err != nil || name != "npm_token" || ref != "env:MY_TOKEN" {
		t.Fatalf("got %q %q %v", name, ref, err)
	}
	name, ref, err = ParseSecretFlag("npm_token=npmrc:/path/.npmrc#registry.example.com")
	if err != nil || name != "npm_token" || ref != "npmrc:/path/.npmrc#registry.example.com" {
		t.Fatalf("got %q %q %v", name, ref, err)
	}

	for _, bad := range []string{
		"noequals",
		"=env:VAR",
		"name=",
		"name=ftp:foo",
		"bad name=env:X",
		"name=npmrc:onlypath",
		"name=npmrc:#host",
	} {
		if _, _, err := ParseSecretFlag(bad); err == nil {
			t.Fatalf("accepted malformed --secret %q", bad)
		} else if strings.Contains(err.Error(), "ftp:foo") || strings.Contains(err.Error(), "onlypath") {
			// design D1: an error never echoes input that did not parse.
			t.Fatalf("error echoes the malformed reference: %v", err)
		}
	}
}

// Task 6.1: env-backed secret resolution, and a missing variable names the
// secret and the reference, never a value.
func TestResolveSecretEnv(t *testing.T) {
	t.Setenv("SF_TEST_RESOLVE_ENV", "canary-env-value")
	v, err := ResolveSecret("npm_token", "env:SF_TEST_RESOLVE_ENV")
	if err != nil || v != "canary-env-value" {
		t.Fatalf("got %q %v", v, err)
	}

	os.Unsetenv("SF_TEST_RESOLVE_MISSING")
	_, err = ResolveSecret("npm_token", "env:SF_TEST_RESOLVE_MISSING")
	if err == nil {
		t.Fatal("accepted an unset environment variable")
	}
	if !strings.Contains(err.Error(), "npm_token") || !strings.Contains(err.Error(), "SF_TEST_RESOLVE_MISSING") {
		t.Fatalf("error must name the secret and the reference: %v", err)
	}
}

// Task 6.1: npmrc-backed secret resolution works for any registry host, and
// a ${VAR} entry inside the npmrc file resolves from the environment.
func TestResolveSecretNpmrcAnyHostWithVarIndirection(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, ".npmrc")
	t.Setenv("SF_TEST_REGISTRY_VAR", "canary-registry-value")
	if err := os.WriteFile(registry, []byte("//registry.example.com/:_authToken=${SF_TEST_REGISTRY_VAR}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := ResolveSecret("npm_token", "npmrc:"+registry+"#registry.example.com")
	if err != nil || v != "canary-registry-value" {
		t.Fatalf("got %q %v", v, err)
	}

	if _, err := ResolveSecret("npm_token", "npmrc:"+registry+"#other-host.example.com"); err == nil {
		t.Fatal("accepted a host the npmrc file has no entry for")
	}

	if _, err := ResolveSecret("npm_token", "npmrc:"+registry); err == nil {
		t.Fatal("accepted a malformed npmrc reference missing #HOST")
	}
}

func TestDockerEnvAddsValuesUnderEveryAliasOnly(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "other")
	env := DockerEnv(map[string]string{"npm_token": "v1", "STRIPE": "v2"})
	has := func(kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	for _, kv := range []string{"npm_token=v1", "NPM_TOKEN=v1", "STRIPE=v2", "COMPOSE_DISABLE_ENV_FILE=1"} {
		if !has(kv) {
			t.Errorf("missing %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
	if has("COMPOSE_PROJECT_NAME=other") {
		t.Error("a COMPOSE_ variable was inherited")
	}
	for _, e := range DockerEnv(nil) {
		if strings.Contains(e, "v1") || strings.HasPrefix(e, "STRIPE=") {
			t.Error("nil values must add nothing")
		}
	}
}

// Review M1: a secret name cannot be one that steers Docker or the shell.
func TestReservedSecretNamesAreRefused(t *testing.T) {
	for _, n := range []string{"COMPOSE_FILE", "COMPOSE_PROJECT_NAME", "DOCKER_HOST", "DOCKER_CONFIG", "PATH", "path", "HOME", "USER", "SHELL", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE"} {
		if ValidSecretName(n) {
			t.Errorf("%s accepted", n)
		}
		if _, _, err := ParseSecretFlag(n + "=env:X"); err == nil {
			t.Errorf("--secret %s accepted", n)
		}
	}
	for _, n := range []string{"NPM_TOKEN", "npm_token", "STRIPE_TEST_KEY", "DOCKERHUB_TOKEN", "COMPOSER_AUTH", "PATHS", "HOMEBREW_TOKEN", "LCX"} {
		if !ValidSecretName(n) {
			t.Errorf("%s refused", n)
		}
	}
}

func TestDockerEnvSkipsReservedSecretNames(t *testing.T) {
	env := DockerEnv(map[string]string{"COMPOSE_FILE": "evil.yml", "DOCKER_HOST": "tcp://evil", "PATH": "/evil", "LC_ALL": "x", "NPM_TOKEN": "ok"})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, bad := range []string{"\nCOMPOSE_FILE=evil.yml\n", "\nDOCKER_HOST=tcp://evil\n", "\nPATH=/evil\n", "\nLC_ALL=x\n"} {
		if strings.Contains(joined, bad) {
			t.Errorf("env holds %q", strings.TrimSpace(bad))
		}
	}
	if !strings.Contains(joined, "\nNPM_TOKEN=ok\n") {
		t.Error("an ordinary name must still be passed")
	}
}
