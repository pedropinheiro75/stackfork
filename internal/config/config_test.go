package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STACKFORK_CONFIG_DIR", dir)
	return dir
}

func TestMissingFileIsEmptyAndDepthDefaults(t *testing.T) {
	tempConfig(t)
	c, err := Load()
	if err != nil || len(c.Roots) != 0 || c.Depth() != DefaultMaxDepth {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestSavePreservesUnknownKeysAndIsPrivate(t *testing.T) {
	dir := tempConfig(t)
	p := filepath.Join(dir, "config.json")
	os.WriteFile(p, []byte(`{"roots":[],"theme":{"x":1},"future":"keep"}`), 0o600)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddExclude("Backup"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)
	if string(m["future"]) != `"keep"` || !strings.Contains(string(m["theme"]), `"x"`) || !strings.Contains(string(m["exclude"]), "Backup") {
		t.Fatalf("lost keys: %s", data)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestTildeExpansionAndRootValidation(t *testing.T) {
	tempConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, "Work", "sub"), 0o755)
	os.WriteFile(filepath.Join(home, "file"), nil, 0o600)
	c, _ := Load()

	for _, bad := range []string{"/", "~", home, filepath.Join(home, "missing"), filepath.Join(home, "file"), "relative/dir"} {
		if _, err := c.AddRoot(bad); err == nil {
			t.Errorf("AddRoot(%q) should be refused", bad)
		}
	}
	got, err := c.AddRoot("~/code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "/Work") || len(c.Roots) != 1 {
		t.Fatalf("got %q roots=%v", got, c.Roots)
	}
	if _, err := c.AddRoot("~/code/sub"); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("nested root must be refused, got %v", err)
	}
	if _, err := c.AddRoot("~/code"); err == nil {
		t.Fatal("duplicate root must be refused")
	}
	if err := c.RemoveRoot("~/code"); err != nil || len(c.Roots) != 0 {
		t.Fatalf("remove: %v %v", err, c.Roots)
	}
	if _, err := os.Stat(filepath.Join(home, "Work", "sub")); err != nil {
		t.Fatal("removing a root must not touch the disk")
	}
}

func TestExcludeAndDepthValidation(t *testing.T) {
	c := &Config{}
	if c.AddExclude("a/b") == nil || c.AddExclude("") == nil || c.SetMaxDepth(0) == nil || c.SetMaxDepth(13) == nil {
		t.Fatal("expected refusals")
	}
	if c.AddExclude("x") != nil || c.SetMaxDepth(3) != nil || c.Depth() != 3 {
		t.Fatal("expected accepts")
	}
}
