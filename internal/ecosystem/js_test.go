package ecosystem

import (
	"os"
	"path/filepath"
	"testing"
)

func writePkg(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "package.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A dependency bump or a reformat does not change the hash; a scripts
// change does. Only package.json gets this treatment.
func TestJSInputHashReadsOnlyWhatLearnUses(t *testing.T) {
	dir := t.TempDir()
	p := writePkg(t, dir, `{"name":"app","version":"1.0.0","scripts":{"dev":"vite"},"dependencies":{"pg":"8.0.0"}}`)
	base, err := InputHash(p)
	if err != nil {
		t.Fatal(err)
	}

	writePkg(t, dir, "{\n  \"name\": \"app\",\n  \"version\": \"1.0.1\",\n  \"scripts\": {\"dev\": \"vite\"},\n  \"dependencies\": {\"pg\": \"8.1.0\", \"zod\": \"3.0.0\"}\n}")
	if h, _ := InputHash(p); h != base {
		t.Fatalf("a dependency bump must not change the hash")
	}

	writePkg(t, dir, `{"name":"app","scripts":{"dev":"next dev"},"dependencies":{"pg":"8.0.0"}}`)
	if h, _ := InputHash(p); h == base {
		t.Fatalf("a scripts change must change the hash")
	}

	writePkg(t, dir, `{not json`)
	if h, err := InputHash(p); err != nil || h == base {
		t.Fatalf("a broken package.json must hash as changed, got %q %v", h, err)
	}

	other := filepath.Join(dir, "docker-compose.yml")
	os.WriteFile(other, []byte("services: {}\n"), 0o600)
	h1, _ := InputHash(other)
	os.WriteFile(other, []byte("services: {}\n# comment\n"), 0o600)
	if h2, _ := InputHash(other); h2 == h1 {
		t.Fatalf("other files must hash whole")
	}
}
