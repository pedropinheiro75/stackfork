package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLivePortsSurviveRestart covers task 8.5 (design.md D7 layer 2, "Restart
// keeps the URL"): a stable host port must be identical before and after
// down/up, unlike the pre-D7 behaviour where Docker assigned a fresh random
// port on every container creation.
func TestLivePortsSurviveRestart(t *testing.T) {
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

	li := liveInstance{binary: bin, root: root, state: filepath.Join(scratch, "state"), legacyState: filepath.Join(scratch, "legacy-state")}
	defer func() {
		exec.Command(li.binary, li.cli("down", "--volumes")...).Run()
		after := snapshotDocker(t)
		assertSameInventory(t, before, after)
	}()

	li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"))
	li.run(t, "up", "--wait-timeout", "90")
	first := li.run(t, "urls")
	if first == "" || first == "No published endpoints; check status or start the stack.\n" {
		t.Fatalf("expected a published endpoint, got %q", first)
	}

	li.run(t, "down")
	li.run(t, "up", "--wait-timeout", "90")
	second := li.run(t, "urls")

	if first != second {
		t.Fatalf("expected urls to survive down/up unchanged:\nbefore: %q\nafter:  %q", first, second)
	}
	t.Log("PASS: urls identical across down/up (stable host port)")
}
