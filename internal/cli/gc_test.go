package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gcPSLine(project, state string) string {
	return `{"Labels":"io.stackfork.root=/whatever,com.docker.compose.project=` + project + `,com.docker.compose.service=web","State":"` + state + `","Status":"","Ports":""}`
}

// TestGCVolumesRequiresYes covers design.md D8: --volumes needs --yes,
// checked before anything else runs (no docker/inventory interaction at
// all in this test).
func TestGCVolumesRequiresYes(t *testing.T) {
	var out bytes.Buffer
	code, err := cmdGC(&out, &out, []string{"--volumes"}, t.TempDir(), t.TempDir())
	if code != 2 || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

// TestGCDryRunListsButDoesNotRemove covers the default dry-run: an orphan
// (missing-checkout) instance is listed, but nothing is touched.
func TestGCDryRunListsButDoesNotRemove(t *testing.T) {
	installFakeDocker(t)
	legacyBase := t.TempDir()
	deletedRoot := filepath.Join(t.TempDir(), "checkout-that-no-longer-exists")
	project := writeSyntheticLegacyInstance(t, legacyBase, deletedRoot, "main")
	t.Setenv("DOCKER_FAKE_PS", "") // nothing running

	var out bytes.Buffer
	code, err := cmdGC(&out, &out, nil, t.TempDir(), legacyBase)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(out.String(), project) {
		t.Fatalf("expected the orphan listed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Fatalf("expected a dry-run notice:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(legacyBase, project, "manifest.json")); err != nil {
		t.Fatalf("dry run must not remove state: %v", err)
	}
}

// TestGCNeverTouchesRunningInstance covers the explicit guard: even with
// --apply --volumes --yes, a project with any running container is
// skipped entirely, never listed and never removed.
func TestGCNeverTouchesRunningInstance(t *testing.T) {
	installFakeDocker(t)
	legacyBase := t.TempDir()
	deletedRoot := filepath.Join(t.TempDir(), "checkout-that-no-longer-exists")
	project := writeSyntheticLegacyInstance(t, legacyBase, deletedRoot, "main")
	t.Setenv("DOCKER_FAKE_PS", gcPSLine(project, "running")+"\n")

	var out bytes.Buffer
	code, err := cmdGC(&out, &out, []string{"--apply", "--volumes", "--yes"}, t.TempDir(), legacyBase)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if strings.Contains(out.String(), project) {
		t.Fatalf("expected the running instance never mentioned:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "nothing to collect") {
		t.Fatalf("expected nothing to collect:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(legacyBase, project, "manifest.json")); err != nil {
		t.Fatalf("running instance's state must survive: %v", err)
	}
}

// TestGCApplyRemovesOrphanState covers --apply actually removing an
// orphan's state directory once `down` succeeds, without --volumes unless
// asked.
func TestGCApplyRemovesOrphanState(t *testing.T) {
	log := installFakeDocker(t)
	legacyBase := t.TempDir()
	deletedRoot := filepath.Join(t.TempDir(), "checkout-that-no-longer-exists")
	project := writeSyntheticLegacyInstance(t, legacyBase, deletedRoot, "main")
	t.Setenv("DOCKER_FAKE_PS", "")

	var out bytes.Buffer
	code, err := cmdGC(&out, &out, []string{"--apply"}, t.TempDir(), legacyBase)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out.String())
	}
	if !strings.Contains(out.String(), "removed "+project) {
		t.Fatalf("expected a removal confirmation:\n%s", out.String())
	}
	if _, statErr := os.Stat(filepath.Join(legacyBase, project)); !os.IsNotExist(statErr) {
		t.Fatalf("expected the state directory gone, stat err=%v", statErr)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "--volumes") {
		t.Fatalf("expected down to run without --volumes:\n%s", data)
	}
}

// TestGCIsPruneOrphaned covers the alias: --apply removes an orphan and
// keeps its volumes; --apply --volumes --yes removes them too; neither ever
// touches the stopped, still-valid instance.
func TestGCIsPruneOrphaned(t *testing.T) {
	for _, withVolumes := range []bool{false, true} {
		m := newMachine(t)
		args := []string{"--apply"}
		if withVolumes {
			args = append(args, "--volumes", "--yes")
		}
		var out bytes.Buffer
		code, err := cmdGC(&out, &out, args, m.base, m.legacy)
		if err != nil || code != 0 {
			t.Fatalf("code=%d err=%v\n%s", code, err, out.String())
		}
		rm := m.f.removals(t)
		if !hasRemoval(rm, "c-orphan") || hasRemoval(rm, "c-stopped") || hasRemoval(rm, "c-running") {
			t.Fatalf("withVolumes=%v: %v", withVolumes, rm)
		}
		if got := hasRemoval(rm, m.orphan+"_pgdata"); got != withVolumes {
			t.Fatalf("withVolumes=%v but volume removed=%v: %v", withVolumes, got, rm)
		}
	}
}
