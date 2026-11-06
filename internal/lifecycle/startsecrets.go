package lifecycle

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/pedropinheiro75/stackfork/internal/compose"
)

// StartSecrets is the secrets step of a per-service Start (the CLI's `start`
// and the dashboard's service-start). It records no run. The step fails the
// Start only when a service it starts lists a secret; the lines are written
// to out only then. Otherwise a failure is ignored and the values are empty:
// nothing resolves later (review I1), and Execute passes Compose none.
func StartSecrets(out io.Writer, root, instanceDir string, targets []string, dashboard bool) (SecretValues, error) {
	var lines bytes.Buffer
	values, err := InstancePreflight(nil, &lines, root, instanceDir, dashboard)
	if err == nil {
		return values, nil
	}
	if !servicesListSecret(instanceDir, targets) {
		return SecretValues{}, nil
	}
	out.Write(lines.Bytes())
	return nil, err
}

// servicesListSecret reports whether one of the services lists a secret in the
// prepared snapshot (build secrets do not count: a start builds nothing). An
// unreadable snapshot counts as a yes: the step stays as strict as before.
func servicesListSecret(instanceDir string, services []string) bool {
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return true
	}
	parsed, err := compose.DecodeJSON(data)
	if err != nil {
		return true
	}
	m, _ := parsed.(map[string]interface{})
	all, _ := m["services"].(map[string]interface{})
	for _, name := range services {
		svc, _ := all[name].(map[string]interface{})
		if list, _ := svc["secrets"].([]interface{}); len(list) > 0 {
			return true
		}
	}
	return false
}
