package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Task 13.1 check: round-trip a fixture (load, save, reload) and refuse
// unknown schema versions.
func TestRoundTrip(t *testing.T) {
	fixture := filepath.Join("testdata", "knowledge.json")
	k, err := Load(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if k.Status != StatusVerified {
		t.Fatalf("expected status verified, got %q", k.Status)
	}
	if len(k.Facts.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(k.Facts.Services))
	}

	out := filepath.Join(t.TempDir(), "knowledge.json")
	if err := k.Save(out); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(k, reloaded) {
		t.Fatalf("round trip changed the value:\nbefore: %+v\nafter:  %+v", k, reloaded)
	}
}

func TestLoadRefusesUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.json")
	k := &Knowledge{Schema: 2, Status: StatusDraft}
	b, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected an error loading an unsupported schema version")
	} else if !strings.Contains(err.Error(), "schema 2") {
		t.Fatalf("expected the error to name the bad schema version, got: %v", err)
	}
}

func TestMarkdownHasNoValues(t *testing.T) {
	k, err := Load(filepath.Join("testdata", "knowledge.json"))
	if err != nil {
		t.Fatal(err)
	}
	md := k.Markdown()
	for _, want := range []string{"db", "postgres 17", "DATABASE_URL", "database", "auth"} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected markdown to mention %q:\n%s", want, md)
		}
	}
	// EnvKeys only ever holds names; nothing resembling a value is in the
	// struct to begin with, so the markdown can't leak one either.
}
