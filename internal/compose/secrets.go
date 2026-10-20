package compose

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/secretstore"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidSecretName reports whether name is a valid secret name.
func ValidSecretName(name string) bool { return secretNamePattern.MatchString(name) }

// ParseSecretFlag splits a --secret NAME=REF argument (design.md D6) into
// its name and reference, validating only the shape (env:VAR, npmrc:PATH#HOST,
// keychain:ITEM or op://VAULT/ITEM/FIELD), never resolving a value. Errors
// never echo the input: it may be a value pasted by mistake. They name the
// secret only when the name is itself a valid name.
func ParseSecretFlag(raw string) (name, ref string, err error) {
	name, ref, ok := strings.Cut(raw, "=")
	if !ok || name == "" || ref == "" {
		return "", "", fmt.Errorf("--secret: expected NAME=REF where REF is env:VAR, npmrc:PATH#HOST, keychain:ITEM or op://VAULT/ITEM/FIELD")
	}
	if !ValidSecretName(name) {
		return "", "", fmt.Errorf("--secret: secret name must match [A-Za-z_][A-Za-z0-9_]*")
	}
	if err := ValidateSecretRef(ref); err != nil {
		return "", "", fmt.Errorf("--secret %s: %w", name, err)
	}
	return name, ref, nil
}

// ValidateSecretRef is the one validator for references from flags, the
// machine map and the manifest. Its errors never contain ref.
func ValidateSecretRef(ref string) error {
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
	case secretstore.IsStoreRef(ref):
		_, _, err := secretstore.ParseRef(ref)
		return err
	default:
		return fmt.Errorf("not a reference: it must start with env:, npmrc:, keychain: or op://")
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
