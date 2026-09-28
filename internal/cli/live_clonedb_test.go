package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cloneDBFixtureComposeYAML = `services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: example
      POSTGRES_DB: app
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 2s
      timeout: 3s
      retries: 30

volumes:
  pgdata:
`

// TestLiveCloneDB covers task 10.3 (design.md D8) against a real Docker
// daemon: two scratch instances of "the same project" (same relative
// Compose filename, different checkouts - exactly the two-worktree
// scenario clone-db exists for), a marker row written into the source's
// Postgres, source stopped, cloned into the target, and the target's own
// Postgres sees that row after starting. Also covers the "target not
// empty without --replace" guard by attempting a second clone in place.
func TestLiveCloneDB(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)

	before := snapshotDocker(t)
	scratch := t.TempDir()
	state := filepath.Join(scratch, "state")
	legacyState := filepath.Join(scratch, "legacy-state")

	srcRoot := filepath.Join(scratch, "src")
	dstRoot := filepath.Join(scratch, "dst")
	for _, root := range []string{srcRoot, dstRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		mustRun(t, "git", "init", "-q", root)
		if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte(cloneDBFixtureComposeYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	src := liveInstance{binary: bin, root: srcRoot, state: state, legacyState: legacyState}
	dst := liveInstance{binary: bin, root: dstRoot, state: state, legacyState: legacyState}

	src.run(t, "prepare", "-f", filepath.Join(srcRoot, "compose.yaml"))
	src.run(t, "up", "--wait-timeout", "90")
	srcProject, _ := src.where(t)["project"].(string)
	if srcProject == "" {
		t.Fatal("expected a source project name")
	}

	// Marker row only the source has.
	mustRun(t, bin, src.cli("exec", "db", "psql", "-U", "postgres", "-d", "app",
		"-c", "CREATE TABLE clone_marker (id int); INSERT INTO clone_marker VALUES (42);")...)

	src.run(t, "down") // stopped, volume kept - clone-db refuses a running source
	dst.run(t, "prepare", "-f", filepath.Join(dstRoot, "compose.yaml"))
	// Target's pgdata volume doesn't exist yet (never `up`), so it's
	// created empty by the copy itself - no --replace needed the first
	// time.
	out := mustRun(t, bin, dst.cli("clone-db", "--from", srcProject)...)
	if !strings.Contains(out, "copied "+srcProject) {
		t.Fatalf("expected a copy confirmation:\n%s", out)
	}
	t.Log("PASS: clone-db copied the source's volume into the fresh target")

	dst.run(t, "up", "--wait-timeout", "90")
	got := mustRun(t, bin, dst.cli("exec", "db", "psql", "-U", "postgres", "-d", "app",
		"-tAc", "SELECT id FROM clone_marker")...)
	if strings.TrimSpace(got) != "42" {
		t.Fatalf("expected the target's Postgres to see the source's row, got %q", got)
	}
	t.Log("PASS: target Postgres has the source's data after cloning")

	// Guard: the target volume is no longer empty (Postgres has written to
	// it), so a second clone without --replace must refuse.
	dst.run(t, "down")
	if out, err := tryRun(bin, dst.cli("clone-db", "--from", srcProject)...); err == nil {
		t.Fatalf("expected a second clone without --replace to refuse, got:\n%s", out)
	} else if !strings.Contains(out, "not empty") {
		t.Fatalf("expected the not-empty refusal, got:\n%s", out)
	}
	t.Log("PASS: a non-empty target is refused without --replace")

	dst.run(t, "down", "--volumes")
	src.run(t, "down", "--volumes")
	after := snapshotDocker(t)
	assertSameInventory(t, before, after)
}
