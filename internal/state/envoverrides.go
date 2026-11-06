package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// EnvOverridesFile is the per-instance file holding environment overrides
// (add-env-overrides D1). It is a file of its own, not a manifest field, so a
// prepare that rewrites the manifest cannot drop it.
const EnvOverridesFile = "env-overrides.json"

const (
	envOverridesVersion = 1
	maxEnvValue         = 4096
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// EnvOverrides maps a service (a Compose service or a host app) to NAME ->
// VALUE pairs that win over the project's environment.
type EnvOverrides struct {
	Version  int                          `json:"version"`
	Services map[string]map[string]string `json:"services"`
}

// ValidEnvName reports why name is not a variable name, or nil.
func ValidEnvName(name string) error {
	if !envNamePattern.MatchString(name) {
		return errors.New("invalid variable name; use letters, digits and underscores, not starting with a digit") // never echoed: it may be a pasted value
	}
	return nil
}

// ValidEnvValue reports why value cannot be stored, or nil.
func ValidEnvValue(value string) error {
	if strings.ContainsAny(value, "\x00\n") {
		return errors.New("a value cannot contain a NUL or a newline")
	}
	if len(value) > maxEnvValue {
		return fmt.Errorf("a value is at most %d bytes", maxEnvValue)
	}
	return nil
}

func (o *EnvOverrides) validate() error {
	for svc, vars := range o.Services {
		if svc == "" {
			return errors.New("an override names no service")
		}
		for n, v := range vars {
			if err := ValidEnvName(n); err != nil {
				return err
			}
			if err := ValidEnvValue(v); err != nil {
				return fmt.Errorf("%s %s: %w", svc, n, err)
			}
		}
	}
	return nil
}

// ReadEnvOverrides loads the instance's overrides; no file means none.
func ReadEnvOverrides(instanceDir string) (*EnvOverrides, error) {
	o := &EnvOverrides{Version: envOverridesVersion, Services: map[string]map[string]string{}}
	b, err := os.ReadFile(filepath.Join(instanceDir, EnvOverridesFile))
	if os.IsNotExist(err) {
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, o); err != nil {
		return nil, fmt.Errorf("%s: %w", EnvOverridesFile, err)
	}
	if o.Services == nil {
		o.Services = map[string]map[string]string{}
	}
	if err := o.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", EnvOverridesFile, err)
	}
	return o, nil
}

// Write stores the overrides (0600, atomic), or removes the file when none
// are left. The caller holds the instance lock.
func (o *EnvOverrides) Write(instanceDir string) error {
	if err := o.validate(); err != nil {
		return err
	}
	path := filepath.Join(instanceDir, EnvOverridesFile)
	if o.Count() == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	o.Version = envOverridesVersion
	return WritePrivate(path, o)
}

// Set stores one override after validating it.
func (o *EnvOverrides) Set(service, name, value string) error {
	if service == "" {
		return errors.New("a service name is required")
	}
	if err := ValidEnvName(name); err != nil {
		return err
	}
	if err := ValidEnvValue(value); err != nil {
		return err
	}
	if o.Services == nil {
		o.Services = map[string]map[string]string{}
	}
	if o.Services[service] == nil {
		o.Services[service] = map[string]string{}
	}
	o.Services[service][name] = value
	return nil
}

// Unset removes one override and reports whether it existed.
func (o *EnvOverrides) Unset(service, name string) bool {
	vars, ok := o.Services[service]
	if !ok {
		return false
	}
	if _, ok := vars[name]; !ok {
		return false
	}
	delete(vars, name)
	if len(vars) == 0 {
		delete(o.Services, service)
	}
	return true
}

// ClearService removes every override of one service.
func (o *EnvOverrides) ClearService(service string) { delete(o.Services, service) }

// Count is the number of overridden variables across services.
func (o *EnvOverrides) Count() int {
	n := 0
	for _, vars := range o.Services {
		n += len(vars)
	}
	return n
}

// Pairs are the KEY=VALUE lines of one service, sorted by name; nil when none.
func (o *EnvOverrides) Pairs(service string) []string {
	vars := o.Services[service]
	if len(vars) == 0 {
		return nil
	}
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n + "=" + vars[n]
	}
	return out
}
