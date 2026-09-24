package compose

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	variableRef  = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
	tokenCharset = regexp.MustCompile(`^[A-Za-z0-9_=-]+$`)
)

// RegistryTokenForHost reads the //<host>/:_authToken entry from an
// npmrc-style file (task 6.1: any registry host, not just GitHub's), with an
// optional ${VAR} indirection resolved from the process environment.
func RegistryTokenForHost(path, host string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("//%s/:_authToken", host)
	value := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		k, candidate, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if strings.TrimSpace(k) == key {
			value = strings.Trim(strings.TrimSpace(candidate), "\"'")
		}
	}
	if m := variableRef.FindStringSubmatch(value); m != nil {
		value = os.Getenv(m[1])
	}
	if value == "" || !tokenCharset.MatchString(value) {
		return "", fmt.Errorf("npmrc has no usable token for %s; supply a literal or environment-reference token entry", host)
	}
	return value, nil
}

// RegistryToken mirrors Python's registry_token(path): reads only the
// //npm.pkg.github.com/:_authToken entry from an npmrc-style file. Kept for
// compatibility; new code resolves through ResolveSecret.
func RegistryToken(path string) (string, error) {
	return RegistryTokenForHost(path, "npm.pkg.github.com")
}
