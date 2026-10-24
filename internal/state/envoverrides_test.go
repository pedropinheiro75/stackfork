package state

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnvOverridesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	o, err := ReadEnvOverrides(dir)
	if err != nil || o.Count() != 0 {
		t.Fatalf("missing file: %v %d", err, o.Count())
	}
	if err := o.Set("web", "ENABLE_GUEST_ACCESS", "true"); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("web", "EMPTY", ""); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("app", "VITE_X", "1"); err != nil {
		t.Fatal(err)
	}
	if err := o.Write(dir); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEnvOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Count() != 3 || got.Services["web"]["ENABLE_GUEST_ACCESS"] != "true" || got.Version != 1 {
		t.Errorf("round trip: %+v", got)
	}
	if v, ok := got.Services["web"]["EMPTY"]; !ok || v != "" {
		t.Errorf("empty value lost: %+v", got)
	}
	if got.Unset("web", "EMPTY"); got.Count() != 2 {
		t.Errorf("unset: %d", got.Count())
	}
	got.ClearService("app")
	if _, ok := got.Services["app"]; ok {
		t.Error("service not cleared")
	}
	if got.Unset("web", "NOPE") {
		t.Error("unset of a missing name reported true")
	}
}

func TestEnvOverridesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes")
	}
	dir := t.TempDir()
	o, _ := ReadEnvOverrides(dir)
	o.Set("web", "A", "b")
	if err := o.Write(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, EnvOverridesFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v %v", fi, err)
	}
}

func TestEnvOverridesWriteEmptyRemovesFile(t *testing.T) {
	dir := t.TempDir()
	o, _ := ReadEnvOverrides(dir)
	o.Set("web", "A", "b")
	o.Write(dir)
	o.Unset("web", "A")
	if err := o.Write(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, EnvOverridesFile)); !os.IsNotExist(err) {
		t.Errorf("file still there: %v", err)
	}
}

func TestEnvOverridesValidation(t *testing.T) {
	o := &EnvOverrides{}
	for _, name := range []string{"", "1A", "A-B", "A B", "A=B", "ä"} {
		if err := o.Set("web", name, "x"); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	for _, v := range []string{"a\x00b", "a\nb", strings.Repeat("x", 4097)} {
		if err := o.Set("web", "A", v); err == nil {
			t.Errorf("value of %d bytes accepted", len(v))
		}
	}
	if err := o.Set("web", "A", strings.Repeat("x", 4096)); err != nil {
		t.Errorf("4096 bytes refused: %v", err)
	}
	if err := o.Set("", "A", "x"); err == nil {
		t.Error("empty service accepted")
	}
}

func TestEnvOverridesRejectsBadFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, EnvOverridesFile), []byte(`{"version":1,"services":{"web":{"bad name":"x"}}}`), 0o600)
	if _, err := ReadEnvOverrides(dir); err == nil {
		t.Error("hand-edited bad name accepted")
	}
}

func TestEnvOverridesPairs(t *testing.T) {
	o := &EnvOverrides{}
	o.Set("web", "B", "2")
	o.Set("web", "A", "1")
	got := strings.Join(o.Pairs("web"), ",")
	if got != "A=1,B=2" || o.Pairs("none") != nil {
		t.Errorf("pairs %q", got)
	}
}
