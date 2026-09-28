package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveGCRemovesOrphan covers task 10.2 (design.md D8) against a real
// Docker daemon: a scratch instance whose checkout directory is deleted
// while its containers still exist becomes exactly the missing-checkout
// case `gc` targets. Dry-run must report it without touching anything;
// --apply must stop and remove it, leaving the machine's Docker state
// identical to before the instance ever existed.
func TestLiveGCRemovesOrphan(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	modRoot := moduleRoot(t)

	before := snapshotDocker(t)
	scratch := t.TempDir()
	root := filepath.Join(scratch, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "-q", root)
	data, err := os.ReadFile(filepath.Join(modRoot, "testdata", "pg17-fixed-name", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	state := filepath.Join(scratch, "state")
	legacyState := filepath.Join(scratch, "legacy-state")
	li := liveInstance{binary: bin, root: root, state: state, legacyState: legacyState}
	li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"))
	li.run(t, "up", "--wait-timeout", "90")
	whereInfo := li.where(t)
	project, _ := whereInfo["project"].(string)
	if project == "" {
		t.Fatalf("expected a project name from where: %+v", whereInfo)
	}

	// Stopped (gc never touches a running instance, checkout or not - own
	// guard, covered separately at the unit level), then the checkout is
	// gone; its volumes and state are not - exactly a missing-checkout
	// instance, never reachable again by any normal command since --root
	// can no longer resolve to a Git checkout.
	li.run(t, "down")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	gcArgs := func(args ...string) []string {
		return append([]string{"--state-dir", state, "--legacy-state-dir", legacyState, "gc"}, args...)
	}

	dryRun := mustRun(t, bin, gcArgs()...)
	if !strings.Contains(dryRun, project) {
		t.Fatalf("expected the orphan listed in dry run:\n%s", dryRun)
	}
	if !strings.Contains(dryRun, "dry run") {
		t.Fatalf("expected a dry-run notice:\n%s", dryRun)
	}
	if _, err := os.Stat(filepath.Join(state, project)); err != nil {
		t.Fatalf("dry run must not remove state: %v", err)
	}
	t.Log("PASS: dry run lists the orphan and touches nothing")

	applied := mustRun(t, bin, gcArgs("--apply", "--volumes", "--yes")...)
	if !strings.Contains(applied, "removed "+project) {
		t.Fatalf("expected a removal confirmation:\n%s", applied)
	}
	if _, statErr := os.Stat(filepath.Join(state, project)); !os.IsNotExist(statErr) {
		t.Fatalf("expected the state directory gone, stat err=%v", statErr)
	}
	t.Log("PASS: --apply --volumes removed the orphan's state")

	after := snapshotDocker(t)
	assertSameInventory(t, before, after)
	t.Log("PASS: machine's Docker state matches pre-test baseline")
}
