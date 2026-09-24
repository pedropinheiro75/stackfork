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
		} else if !strings.Contains(err.Error(), bad) {
			t.Fatalf("expected error to name the malformed flag %q, got %v", bad, err)
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
