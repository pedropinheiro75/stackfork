package learn

import (
	"os"
	"path/filepath"
	"sort"
)

// migrationCandidates are the directories a project commonly keeps schema
// migrations in (live-mode D11).
var migrationCandidates = []string{
	"prisma/migrations", "db/migrate", "migrations", "drizzle", "supabase/migrations",
	"alembic/versions", "src/migrations", "database/migrations",
}

// buildContextsUnderRoot returns the build contexts of a rendered Compose
// model that lie inside root, as root-relative slash paths ("." for root).
func buildContextsUnderRoot(rendered interface{}, root string) []string {
	model, _ := rendered.(map[string]interface{})
	svcs, _ := model["services"].(map[string]interface{})
	seen := map[string]bool{}
	var out []string
	for _, raw := range svcs {
		svc, _ := raw.(map[string]interface{})
		var ctx string
		switch b := svc["build"].(type) {
		case string:
			ctx = b
		case map[string]interface{}:
			ctx, _ = b["context"].(string)
		}
		if ctx == "" {
			continue
		}
		if !filepath.IsAbs(ctx) {
			ctx = filepath.Join(root, ctx)
		}
		rel, err := filepath.Rel(root, ctx)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		if rel = filepath.ToSlash(rel); !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// detectMigrationDirs lists the candidate directories that exist at the
// root and in each build context (root-relative, slash paths, sorted). It
// only stats; a symbolic link is not followed. It runs no command.
func detectMigrationDirs(root string, contexts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, base := range append([]string{"."}, contexts...) {
		for _, c := range migrationCandidates {
			rel := filepath.ToSlash(filepath.Join(base, c))
			if seen[rel] {
				continue
			}
			if fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && fi.IsDir() {
				seen[rel] = true
				out = append(out, rel)
			}
		}
	}
	sort.Strings(out)
	return out
}
