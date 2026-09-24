package identity

import (
	"os"
	"path/filepath"
	"testing"
)

// Ported from the original Python CLI's check(): checkout+branch identity
// and hostname validation.
func TestProjectNameIdentity(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "company-a", "app")
	b := filepath.Join(root, "company-b", "app")
	if err := os.MkdirAll(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}

	pa, err := ProjectName(a, "feature/demo")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := ProjectName(b, "feature/demo")
	if err != nil {
		t.Fatal(err)
	}
	if pa == pb {
		t.Fatalf("distinct checkouts must produce distinct projects, got %q twice", pa)
	}
	other, err := ProjectName(a, "feature/other")
	if err != nil {
		t.Fatal(err)
	}
	if pa == other {
		t.Fatalf("distinct branches of the same checkout must produce distinct projects")
	}
	if !slugInvalid.MatchString("A") { // sanity: uppercase must be normalised
		t.Fatalf("slug regex should treat uppercase as invalid")
	}

	ha, err := BrowserHostname(pa, "")
	if err != nil {
		t.Fatal(err)
	}
	hb, err := BrowserHostname(pb, "")
	if err != nil {
		t.Fatal(err)
	}
	if ha == hb {
		t.Fatalf("distinct projects must yield distinct default hostnames")
	}

	for _, invalid := range []string{"localhost", "foo.example.com", "-bad.localhost",
		"a.b.localhost", string(make([]byte, 64)) + ".localhost"} {
		if _, err := BrowserHostname(pa, invalid); err == nil {
			t.Fatalf("accepted invalid hostname %q", invalid)
		}
	}
}

func TestProjectNameSlugFallback(t *testing.T) {
	// A basename with nothing but disallowed characters falls back to "app".
	root := t.TempDir()
	weird := filepath.Join(root, "---")
	if err := os.MkdirAll(weird, 0o755); err != nil {
		t.Fatal(err)
	}
	name, err := ProjectName(weird, "main")
	if err != nil {
		t.Fatal(err)
	}
	if name[:len(Prefix)+5] != Prefix+"-app-" {
		t.Fatalf("expected app fallback slug, got %q", name)
	}
}

func TestInstancePattern(t *testing.T) {
	if !InstancePattern.MatchString("idv-app-abc123") {
		t.Fatal("expected valid instance name to match")
	}
	if InstancePattern.MatchString("../etc/passwd") {
		t.Fatal("must reject a non idv-[a-z0-9-]+ instance name")
	}
}
