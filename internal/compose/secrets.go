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

// ParseCommandSecretFlag is ParseSecretFlag for the --secret flag of a
// command (prepare, start-env): only env: and npmrc: references. keychain:
// and op:// come from the machine map, which only a person writes (design
// D5, D12); a flag an agent can add would point a name at any store item.
// The error never echoes the reference.
func ParseCommandSecretFlag(raw string) (name, ref string, err error) {
	name, ref, err = ParseSecretFlag(raw)
	if err != nil {
		if n, r, ok := strings.Cut(raw, "="); ok && ValidSecretName(n) && (strings.HasPrefix(r, "keychain:") || strings.HasPrefix(r, "op://")) {
			return "", "", storeFlagError(n)
		}
		return "", "", err
	}
	if secretstore.IsStoreRef(ref) {
		return "", "", storeFlagError(name)
	}
	return name, ref, nil
}

func storeFlagError(name string) error {
	return fmt.Errorf("--secret %s: keychain: and op:// references are not accepted as a flag; they come only from the machine map, which a person writes: stackfork secret map REPO %s=REF", name, name)
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

// CheckSecret resolves a reference of any kind into a value and a status
// (design D4). The error says why the status is not Resolves, never a value.
func CheckSecret(ref string) (string, secretstore.Status, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		v := strings.TrimPrefix(ref, "env:")
		value := os.Getenv(v)
		if value == "" {
			return "", secretstore.Missing, fmt.Errorf("environment variable %s is not set", v)
		}
		return value, secretstore.Resolves, nil
	case strings.HasPrefix(ref, "npmrc:"):
		path, host, ok := strings.Cut(strings.TrimPrefix(ref, "npmrc:"), "#")
		if !ok || path == "" || host == "" {
			return "", secretstore.Error, fmt.Errorf("malformed npmrc reference, expected npmrc:PATH#HOST")
		}
		value, err := RegistryTokenForHost(path, host)
		if err != nil {
			return "", secretstore.Missing, err
		}
		return value, secretstore.Resolves, nil
	case secretstore.IsStoreRef(ref):
		return secretstore.Resolve(ref)
	}
	return "", secretstore.Error, fmt.Errorf("reference must start with env:, npmrc:, keychain: or op://")
}

// ResolveSecret resolves a stored reference (never a value, per
// ParseSecretFlag) into its value, at up/run as design.md D6 requires.
// Errors name the secret and the reference, never a partial value.
func ResolveSecret(name, ref string) (string, error) {
	value, st, err := CheckSecret(ref)
	switch {
	case st == secretstore.Resolves:
		return value, nil
	case secretstore.IsStoreRef(ref):
		return "", fmt.Errorf("secret %s (%s): %s: %v", name, ref, st, err)
	}
	return "", fmt.Errorf("secret %s (%s): %v", name, ref, err)
}

// ExistsSecret reports a reference's status without ever prompting: env: and
// npmrc: are read and the value dropped, a keychain: item is checked without
// reading its value, and op:// is NotChecked (design D4).
func ExistsSecret(ref string) (secretstore.Status, error) {
	if secretstore.IsStoreRef(ref) {
		return secretstore.Exists(ref), nil
	}
	_, st, err := CheckSecret(ref)
	return st, err
}
