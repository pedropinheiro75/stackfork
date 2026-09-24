package compose

import (
	"errors"
	"os"
	"regexp"
	"strings"
)

const registryTokenKey = "//npm.pkg.github.com/:_authToken"

var (
	variableRef  = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
	tokenCharset = regexp.MustCompile(`^[A-Za-z0-9_=-]+$`)
)

// RegistryToken mirrors Python's registry_token(path): reads only the
// //npm.pkg.github.com/:_authToken entry from an npmrc-style file.
func RegistryToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		key, candidate, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if strings.TrimSpace(key) == registryTokenKey {
			value = strings.Trim(strings.TrimSpace(candidate), "\"'")
		}
	}
	if m := variableRef.FindStringSubmatch(value); m != nil {
		value = os.Getenv(m[1])
	}
	if value == "" || !tokenCharset.MatchString(value) {
		return "", errors.New("Selected npmrc has no usable GitHub registry token; supply NPM_TOKEN or a literal/environment-reference token entry")
	}
	return value, nil
}
