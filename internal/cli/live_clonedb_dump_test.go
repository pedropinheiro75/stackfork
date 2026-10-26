package cli

import (
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveCloneDBFromDump covers add-env-and-seed's data-seeding capability
// end to end against a real Postgres: restoring a plain .sql dump, the
// not-empty refusal without --replace, --replace actually clearing the
// target first (never doubling rows), and a .sql.gz dump streamed through
// unmodified - transparently decompressed, nothing ever written to a temp
// file (testdata/clonedb-dump's own compose fixture + seed.sql).
func TestLiveCloneDBFromDump(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	modRoot := moduleRoot(t)
	fixtureDir := filepath.Join(modRoot, "testdata", "clonedb-dump")

	before := snapshotDocker(t)
	scratch := t.TempDir()
	root := filepath.Join(scratch, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	liveInitRepo(t, root)
	composeSrc, err := os.ReadFile(filepath.Join(fixtureDir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), composeSrc, 0o600); err != nil {
		t.Fatal(err)
	}

	state := filepath.Join(scratch, "state")
	li := liveInstance{binary: bin, root: root, state: state}
	defer exec.Command(li.binary, li.cli("down", "--volumes")...).Run() //nolint:errcheck // best-effort cleanup on any exit path

	li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"))
	li.run(t, "up", "--wait-timeout", "90")

	dumpPath := filepath.Join(fixtureDir, "seed.sql")
	countWidgets := func() string {
		out := li.run(t, "exec", "db", "psql", "-U", "postgres", "-d", "app", "-tAc", "select count(*) from widgets")
		return strings.TrimSpace(out)
	}

	// First restore: the table doesn't exist yet, so the empty-check
	// passes trivially and this seeds it for real.
	out := li.run(t, "clone-db", "--from-dump", dumpPath)
	if !strings.Contains(out, "seeded") {
		t.Fatalf("expected a success message, got %q", out)
	}
	if got := countWidgets(); got != "3" {
		t.Fatalf("expected 3 rows after the first restore, got %q", got)
	}
	t.Log("PASS: plain .sql dump restored, row count matches the fixture")

	// Second restore without --replace: the table now has rows, must refuse.
	failOut, err := exec.Command(bin, li.cli("clone-db", "--from-dump", dumpPath)...).CombinedOutput()
	if err == nil || !strings.Contains(string(failOut), "--replace") {
		t.Fatalf("expected a not-empty refusal naming --replace, got err=%v output=%s", err, failOut)
	}
	t.Log("PASS: second restore without --replace refused")

	// --replace: clears first, so the count stays 3, never 6.
	li.run(t, "clone-db", "--from-dump", dumpPath, "--replace")
	if got := countWidgets(); got != "3" {
		t.Fatalf("expected --replace to reset before restoring (still 3 rows, not doubled), got %q", got)
	}
	t.Log("PASS: --replace cleared the target before restoring, no doubled rows")

	// .sql.gz: gzip the same fixture on the fly (no extra testdata file),
	// restore with --replace again - same row count proves the gzip path
	// decompressed correctly rather than feeding compressed bytes to psql.
	gzPath := filepath.Join(scratch, "seed.sql.gz")
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	gzFile, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(gzFile)
	if _, err := gw.Write(raw); err != nil {
		t.Fatal(err)
	}
	gw.Close()
	gzFile.Close()

	li.run(t, "clone-db", "--from-dump", gzPath, "--replace")
	if got := countWidgets(); got != "3" {
		t.Fatalf("expected the gzipped dump to restore the same 3 rows, got %q", got)
	}
	t.Log("PASS: .sql.gz dump transparently decompressed and restored")

	li.run(t, "down", "--volumes")
	after := snapshotDocker(t)
	assertSameInventory(t, before, after)
}
