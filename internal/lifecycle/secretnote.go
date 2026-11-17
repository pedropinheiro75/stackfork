package lifecycle

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
)

// secretServices are the services of the prepared snapshot that list a secret
// (build secrets do not count), sorted. A missing or unreadable snapshot gives
// none.
func secretServices(instanceDir string) []string {
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil
	}
	parsed, err := compose.DecodeJSON(data)
	if err != nil {
		return nil
	}
	m, _ := parsed.(map[string]interface{})
	all, _ := m["services"].(map[string]interface{})
	var out []string
	for name, raw := range all {
		svc, _ := raw.(map[string]interface{})
		if list, _ := svc["secrets"].([]interface{}); len(list) > 0 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// runningSecretServices are the services that list a secret and have a running
// container now. Best effort: a Docker error counts as none.
func runningSecretServices(instanceDir, project string) []string {
	var out []string
	for _, svc := range secretServices(instanceDir) {
		b, err := exec.Command("docker", "ps", "-q",
			"--filter", "label=com.docker.compose.project="+project,
			"--filter", "label=com.docker.compose.service="+svc).Output()
		if err == nil && strings.TrimSpace(string(b)) != "" {
			out = append(out, svc)
		}
	}
	return out
}

// NoteSecretsKeepOldValue tells the person, before an `up`, that a container
// that is already running keeps the secret values it started with: Compose
// does not recreate it when only a value changed, and stackfork keeps no
// record of values to compare (see SECURITY.md). It prints nothing when no
// such container runs. It never reads or prints a value.
func NoteSecretsKeepOldValue(out io.Writer, instanceDir, project string) {
	running := runningSecretServices(instanceDir, project)
	if len(running) == 0 {
		return
	}
	list := strings.Join(running, " ")
	fmt.Fprintf(out, "note: %s already runs and keeps the secret values it started with. If a value changed, run: stackfork stop %s && stackfork start %s\n", list, list, list)
}
