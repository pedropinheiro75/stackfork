package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

// writeComposeFile writes a minimal source Compose file (the kind
// LiteralPassword itself scans, distinct from the rendered compose.json)
// and returns its absolute path.
func writeComposeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- negative cases (written first: these are the property that matters) --

// TestLiteralPasswordRefusesInterpolation covers D3b: a `${VAR}` value
// survives literally in compose.json (prepare always renders with
// --no-env-resolution) and must never be returned, whatever the source of
// VAR (including a --secret).
func TestLiteralPasswordRefusesInterpolation(t *testing.T) {
	dir := t.TempDir()
	files := []string{writeComposeFile(t, dir, "compose.yaml", `
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: ${DB_PASSWORD}
`)}
	raw := map[string]interface{}{
		"environment": map[string]interface{}{"POSTGRES_PASSWORD": "${DB_PASSWORD}"},
	}
	if pw, ok := LiteralPassword(files, "db", "postgres", raw); ok {
		t.Fatalf("expected refusal for interpolated value, got %q", pw)
	}
}

// TestLiteralPasswordRefusesEnvFile covers D3b: a service that loads any
// env_file must never have a password returned for it, even though the
// rendered compose.json's environment map looks identical to a literal
// value (env_file entries get merged into it the same way).
func TestLiteralPasswordRefusesEnvFile(t *testing.T) {
	dir := t.TempDir()
	files := []string{writeComposeFile(t, dir, "compose.yaml", `
services:
  db:
    image: postgres:17
    env_file:
      - .env.db
    environment:
      POSTGRES_USER: appuser
`)}
	// The rendered compose.json would show the env_file-sourced password
	// as a plain, non-${} string - indistinguishable from a literal value
	// without the source-file env_file check.
	raw := map[string]interface{}{
		"environment": map[string]interface{}{"POSTGRES_PASSWORD": "leaked-from-env-file"},
	}
	if pw, ok := LiteralPassword(files, "db", "postgres", raw); ok {
		t.Fatalf("expected refusal for env_file-backed service, got %q", pw)
	}
}

// TestLiteralPasswordRefusesUnknownKind covers an unsupported/unknown kind
// (e.g. "mail", "neo4j") never returning anything.
func TestLiteralPasswordRefusesUnknownKind(t *testing.T) {
	raw := map[string]interface{}{
		"environment": map[string]interface{}{"POSTGRES_PASSWORD": "whatever"},
	}
	if pw, ok := LiteralPassword(nil, "db", "mail", raw); ok {
		t.Fatalf("expected refusal for unknown kind, got %q", pw)
	}
}

// TestLiteralPasswordRefusesEmptyOrMissing covers an absent or empty value.
func TestLiteralPasswordRefusesEmptyOrMissing(t *testing.T) {
	dir := t.TempDir()
	files := []string{writeComposeFile(t, dir, "compose.yaml", `
services:
  db:
    image: postgres:17
`)}
	raw := map[string]interface{}{"environment": map[string]interface{}{}}
	if pw, ok := LiteralPassword(files, "db", "postgres", raw); ok {
		t.Fatalf("expected refusal for missing value, got %q", pw)
	}
	raw2 := map[string]interface{}{"environment": map[string]interface{}{"POSTGRES_PASSWORD": ""}}
	if pw, ok := LiteralPassword(files, "db", "postgres", raw2); ok {
		t.Fatalf("expected refusal for empty value, got %q", pw)
	}
}

// TestLiteralPasswordScopedToService proves the env_file scan is scoped to
// the requested service: a sibling service's env_file must not block this
// service's literal, in-YAML password.
func TestLiteralPasswordScopedToService(t *testing.T) {
	dir := t.TempDir()
	files := []string{writeComposeFile(t, dir, "compose.yaml", `
services:
  other:
    image: redis:7
    env_file:
      - .env.other
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: appuser
      POSTGRES_PASSWORD: literal-in-yaml
`)}
	raw := map[string]interface{}{
		"environment": map[string]interface{}{"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "literal-in-yaml"},
	}
	pw, ok := LiteralPassword(files, "db", "postgres", raw)
	if !ok || pw != "literal-in-yaml" {
		t.Fatalf("expected the literal value for the unaffected sibling service, got %q, %v", pw, ok)
	}
}

// --- positive cases --------------------------------------------------------

func TestLiteralPasswordLiteralValue(t *testing.T) {
	dir := t.TempDir()
	files := []string{writeComposeFile(t, dir, "compose.yaml", `
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: appuser
      POSTGRES_PASSWORD: super-secret-123
`)}
	raw := map[string]interface{}{
		"environment": map[string]interface{}{"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "super-secret-123"},
	}
	pw, ok := LiteralPassword(files, "db", "postgres", raw)
	if !ok || pw != "super-secret-123" {
		t.Fatalf("expected literal password, got %q, %v", pw, ok)
	}
}

func TestLiteralPasswordRedisRequirepass(t *testing.T) {
	dir := t.TempDir()
	files := []string{writeComposeFile(t, dir, "compose.yaml", `
services:
  cache:
    image: redis:7
    command: ["redis-server", "--requirepass", "cache-secret"]
`)}
	raw := map[string]interface{}{
		"command": []interface{}{"redis-server", "--requirepass", "cache-secret"},
	}
	pw, ok := LiteralPassword(files, "cache", "redis", raw)
	if !ok || pw != "cache-secret" {
		t.Fatalf("expected requirepass value, got %q, %v", pw, ok)
	}
}

func TestLiteralPasswordRedisRequirepassInterpolatedRefused(t *testing.T) {
	dir := t.TempDir()
	files := []string{writeComposeFile(t, dir, "compose.yaml", `
services:
  cache:
    image: redis:7
    command: ["redis-server", "--requirepass", "${REDIS_PASSWORD}"]
`)}
	raw := map[string]interface{}{
		"command": []interface{}{"redis-server", "--requirepass", "${REDIS_PASSWORD}"},
	}
	if pw, ok := LiteralPassword(files, "cache", "redis", raw); ok {
		t.Fatalf("expected refusal for interpolated requirepass, got %q", pw)
	}
}

// TestLiteralPasswordFailsClosedWhenSourceUnreadable covers the fail-
// closed contract itself: no files, or a file that can't be read, must
// refuse exactly like a real env_file would - never silently proceed as
// if the source were clean.
func TestLiteralPasswordFailsClosedWhenSourceUnreadable(t *testing.T) {
	raw := map[string]interface{}{
		"environment": map[string]interface{}{"POSTGRES_PASSWORD": "literal-value"},
	}
	if pw, ok := LiteralPassword(nil, "db", "postgres", raw); ok {
		t.Fatalf("expected refusal with no source files, got %q", pw)
	}
	if pw, ok := LiteralPassword([]string{"/nonexistent/compose.yaml"}, "db", "postgres", raw); ok {
		t.Fatalf("expected refusal with an unreadable source file, got %q", pw)
	}
}

// TestLiteralPasswordMultipleFilesLastWins mirrors Compose's own override
// semantics: a later -f file's value is the one actually rendered, so it
// must be the one considered literal (with the env_file gate keyed off
// wherever the service block last appears too).
func TestLiteralPasswordMultipleFilesLastWins(t *testing.T) {
	dir := t.TempDir()
	base := writeComposeFile(t, dir, "compose.yaml", `
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_PASSWORD: base-value
`)
	override := writeComposeFile(t, dir, "compose.override.yaml", `
services:
  db:
    environment:
      POSTGRES_PASSWORD: override-value
`)
	raw := map[string]interface{}{
		"environment": map[string]interface{}{"POSTGRES_PASSWORD": "override-value"},
	}
	pw, ok := LiteralPassword([]string{base, override}, "db", "postgres", raw)
	if !ok || pw != "override-value" {
		t.Fatalf("expected the last file's literal value, got %q, %v", pw, ok)
	}
}
