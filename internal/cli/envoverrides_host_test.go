package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An override of a host app comes after the instance's block and after the
// inherited environment; STACKFORK_HOST_RUN stays last (add-env-overrides D2).
func TestHostAppGetsItsOverrides(t *testing.T) {
	t.Setenv("STACKFORK_PORT_RANGE_START", "31500")
	t.Setenv("STACKFORK_PORT_RANGE_END", "31999")
	t.Setenv("INHERITED_FLAG", "from-os")
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	dir := filepath.Join(stateDir, project)
	writeOverrides(t, dir, "app", "DATABASE_URL", "postgres://override", "app", "INHERITED_FLAG", "from-override",
		"app", "ENABLE_GUEST_ACCESS", "true", "other", "NOT_FOR_APP", "x")
	out := filepath.Join(t.TempDir(), "env.txt")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", "sh", "-c", "env > "+out); code != 0 {
		t.Fatalf("host: %s", stderr)
	}
	b, _ := os.ReadFile(out)
	text := string(b)
	for _, want := range []string{"DATABASE_URL=postgres://override\n", "INHERITED_FLAG=from-override\n", "ENABLE_GUEST_ACCESS=true\n", "STACKFORK_HOST_RUN=1\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "NOT_FOR_APP") {
		t.Error("another service's override leaked")
	}
}

func TestHostChildEnvOrder(t *testing.T) {
	dir := t.TempDir()
	writeOverrides(t, dir, "app", "A", "override")
	env := hostChildEnv(dir, "app", "A=block\nB=block", "PORT=1")
	var order []string
	for _, e := range env {
		if strings.HasPrefix(e, "A=") || strings.HasPrefix(e, "PORT=") || strings.HasPrefix(e, "STACKFORK_HOST_RUN=") {
			order = append(order, e)
		}
	}
	if got := strings.Join(order, ","); got != "A=block,A=override,PORT=1,STACKFORK_HOST_RUN=1" {
		t.Errorf("order %s", got)
	}
}
