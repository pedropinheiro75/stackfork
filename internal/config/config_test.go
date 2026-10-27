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
	os.MkdirAll(filepath.Join(home, "code", "sub"), 0o755)
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
	if !strings.HasSuffix(got, "/code") || len(c.Roots) != 1 {
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
	if _, err := os.Stat(filepath.Join(home, "code", "sub")); err != nil {
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

func TestModeDefaultsAndPerRepo(t *testing.T) {
	dir := tempConfig(t)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"future":"keep"}`), 0o600)
	repo := t.TempDir()
	other := t.TempDir()
	c, _ := Load()
	if got := c.ModeFor(repo); got != "container" {
		t.Fatalf("default = %q", got)
	}
	if err := c.SetMode("hybrid"); err != nil {
		t.Fatal(err)
	}
	if c.ModeFor(repo) != "hybrid" || c.ModeFor(other) != "hybrid" {
		t.Fatal("global hybrid must apply to every repo")
	}
	if err := c.SetRepoMode(repo, "container"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c, _ = Load()
	if c.ModeFor(repo) != "container" || c.ModeFor(other) != "hybrid" {
		t.Fatalf("override: %q %q", c.ModeFor(repo), c.ModeFor(other))
	}
	err := c.SetMode("fast")
	if err == nil || !strings.Contains(err.Error(), "container") || !strings.Contains(err.Error(), "hybrid") {
		t.Fatalf("bad mode error = %v", err)
	}
	if c.SetRepoMode(repo, "fast") == nil {
		t.Fatal("bad repo mode accepted")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if strings.Count(string(raw), `"mode"`) != 1 || !strings.Contains(string(raw), `"future"`) {
		t.Fatalf("file: %s", raw)
	}
}

func TestResumeOnStartDefaultsOnAndRoundTrips(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	c, err := Load()
	if err != nil || !c.ResumeEnabled() {
		t.Fatalf("default must be on: %v", err)
	}
	if err := c.SetResumeOnStart("off"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetResumeOnStart("maybe"); err == nil {
		t.Fatal("maybe must be refused")
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(); err != nil || c.ResumeEnabled() {
		t.Fatalf("off must persist: %v", err)
	}
}
