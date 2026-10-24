package inventory

import (
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// The card shows "N env overrides": the instance carries the count and no value.
func TestInstanceCountsEnvOverrides(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-env-1", root, "main", []string{"web"}, nil)
	if i := collectOne(t, base); i.EnvOverrides != 0 {
		t.Fatalf("no file, no count: %d", i.EnvOverrides)
	}
	dir := filepath.Join(base, "sf-env-1")
	o, _ := state.ReadEnvOverrides(dir)
	o.Set("web", "ENABLE_X", "false")
	o.Set("web", "LOG_LEVEL", "debug")
	if err := o.Write(dir); err != nil {
		t.Fatal(err)
	}
	if i := collectOne(t, base); i.EnvOverrides != 2 {
		t.Fatalf("want 2, got %d", i.EnvOverrides)
	}
}
