package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveOnOffAndSymlink(t *testing.T) {
	tempConfig(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	c, _ := Load()
	if c.LiveFor(real) {
		t.Fatal("default is off")
	}
	if err := c.SetLive(link); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || !c.LiveFor(real) || !c.LiveFor(link) || len(c.LiveRepos()) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	c.UnsetLive(link)
	_ = c.Save()
	c, _ = Load()
	if c.LiveFor(real) || len(c.Live) != 0 {
		t.Fatalf("%+v", c.Live)
	}
}

func TestLiveOldConfigAndUnknownKeysKept(t *testing.T) {
	p := writeCfg(t, `{"roots":[],"terminal":"warp"}`)
	c, err := Load()
	if err != nil || c.LiveFor("/x") {
		t.Fatalf("%v", err)
	}
	_ = c.SetLive(t.TempDir())
	_ = c.Save()
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"terminal"`) || !strings.Contains(string(b), `"live"`) {
		t.Fatalf("%s", b)
	}
}

func TestCleanupDefaultsAndBounds(t *testing.T) {
	writeCfg(t, `{"roots":[]}`)
	c, _ := Load()
	repo := t.TempDir()
	if c.CleanupFor(repo).Mode != CleanupOff {
		t.Fatal("off when live is off")
	}
	_ = c.SetLive(repo)
	if got := c.CleanupFor(repo); got != (Cleanup{Mode: "suggest", GraceHours: 24}) {
		t.Fatalf("%+v", got)
	}
	for _, bad := range []Cleanup{{"auto", 0, true}, {"auto", 721, false}, {"nope", 24, false}} {
		if c.SetCleanup(repo, bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := c.SetCleanup(repo, Cleanup{"auto", 720, true}); err != nil {
		t.Fatal(err)
	}
	_ = c.Save()
	c, err := Load()
	if err != nil || c.CleanupFor(repo) != (Cleanup{"auto", 720, true}) {
		t.Fatalf("%+v %v", c.Cleanup, err)
	}
	writeCfg(t, `{"cleanup":{"/code/acme-web":{"mode":"auto","grace_hours":0}}}`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "/code/acme-web") {
		t.Fatalf("%v", err)
	}
}
