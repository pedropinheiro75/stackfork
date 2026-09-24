package compose

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseSecretFlag splits a --secret NAME=REF argument (design.md D6) into
// its name and reference, validating only the shape (env:VAR or
// npmrc:PATH#HOST), never resolving a value. Errors name the raw flag and
// the reference, never a value, since REF never carries one.
func ParseSecretFlag(raw string) (name, ref string, err error) {
	name, ref, ok := strings.Cut(raw, "=")
	if !ok || name == "" || ref == "" {
		return "", "", fmt.Errorf("--secret %q: expected NAME=env:VAR or NAME=npmrc:PATH#HOST", raw)
	}
	if !secretNamePattern.MatchString(name) {
		return "", "", fmt.Errorf("--secret %q: secret name %q must match [A-Za-z_][A-Za-z0-9_]*", raw, name)
	}
	if err := validateSecretRef(ref); err != nil {
		return "", "", fmt.Errorf("--secret %q: %w", raw, err)
	}
	return name, ref, nil
}

func validateSecretRef(ref string) error {
	switch {
	case strings.HasPrefix(ref, "env:"):
		if strings.TrimPrefix(ref, "env:") == "" {
			return fmt.Errorf("env reference needs a variable name")
		}
	case strings.HasPrefix(ref, "npmrc:"):
		path, host, ok := strings.Cut(strings.TrimPrefix(ref, "npmrc:"), "#")
		if !ok || path == "" || host == "" {
			return fmt.Errorf("npmrc reference needs PATH#HOST")
		}
	default:
		return fmt.Errorf("reference must start with env: or npmrc:")
	}
	return nil
}

// ResolveSecret resolves a stored reference (never a value, per
// ParseSecretFlag) into its value, at up/run as design.md D6 requires.
// Errors name the secret and the reference, never a partial value.
func ResolveSecret(name, ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		v := strings.TrimPrefix(ref, "env:")
		value := os.Getenv(v)
		if value == "" {
			return "", fmt.Errorf("secret %s (%s): environment variable %s is not set", name, ref, v)
		}
		return value, nil
	case strings.HasPrefix(ref, "npmrc:"):
		path, host, ok := strings.Cut(strings.TrimPrefix(ref, "npmrc:"), "#")
		if !ok || path == "" || host == "" {
			return "", fmt.Errorf("secret %s (%s): malformed npmrc reference, expected npmrc:PATH#HOST", name, ref)
		}
		value, err := RegistryTokenForHost(path, host)
		if err != nil {
			return "", fmt.Errorf("secret %s (%s): %w", name, ref, err)
		}
		return value, nil
	default:
		return "", fmt.Errorf("secret %s (%s): reference must start with env: or npmrc:", name, ref)
	}
}
