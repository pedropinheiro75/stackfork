package learn

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mk(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectMigrationDirsEachCandidate(t *testing.T) {
	for _, c := range migrationCandidates {
		root := t.TempDir()
		mk(t, root, c)
		if got := detectMigrationDirs(root, nil); !reflect.DeepEqual(got, []string{c}) {
			t.Errorf("%s: %v", c, got)
		}
	}
}

func TestDetectMigrationDirsNoneAndFileAndLink(t *testing.T) {
	root := t.TempDir()
	if got := detectMigrationDirs(root, nil); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "migrations"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectMigrationDirs(root, nil); len(got) != 0 {
		t.Fatalf("a file is not a directory: %v", got)
	}
	os.Remove(filepath.Join(root, "migrations"))
	if os.Symlink(t.TempDir(), filepath.Join(root, "migrations")) == nil {
		if got := detectMigrationDirs(root, nil); len(got) != 0 {
			t.Fatalf("a symbolic link is skipped: %v", got)
		}
	}
}

func TestDetectMigrationDirsBuildContexts(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "migrations", "apps/api/prisma/migrations", "apps/web/db/migrate")
	rendered := map[string]interface{}{"services": map[string]interface{}{
		"api": map[string]interface{}{"build": map[string]interface{}{"context": filepath.Join(root, "apps/api")}},
		"web": map[string]interface{}{"build": "apps/web"},
		"db":  map[string]interface{}{"image": "postgres:16"},
		"out": map[string]interface{}{"build": "../elsewhere"},
	}}
	ctx := buildContextsUnderRoot(rendered, root)
	if !reflect.DeepEqual(ctx, []string{"apps/api", "apps/web"}) {
		t.Fatalf("%v", ctx)
	}
	want := []string{"apps/api/prisma/migrations", "apps/web/db/migrate", "migrations"}
	if got := detectMigrationDirs(root, ctx); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestScanListsMigrationDirs(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "drizzle")
	k, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(k.Facts.MigrationDirs, []string{"drizzle"}) {
		t.Fatalf("%v", k.Facts.MigrationDirs)
	}
}
