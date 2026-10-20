package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommandRoundTrip(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("STACKFORK_CONFIG_DIR", cfg)
	root := t.TempDir()

	if out, _, code := runCLI(t, "config", "get"); code != 0 || !strings.Contains(out, `"roots": []`) {
		t.Fatalf("empty get: %d %s", code, out)
	}
	if _, stderr, code := runCLI(t, "config", "add-root", "/"); code == 0 || !strings.Contains(stderr, "filesystem root") {
		t.Fatalf("/ must be refused: %d %s", code, stderr)
	}
	if _, stderr, code := runCLI(t, "config", "add-root", root); code != 0 {
		t.Fatalf("add-root: %s", stderr)
	}
	runCLI(t, "config", "add-exclude", "Backup")
	runCLI(t, "config", "set", "max-depth", "4")
	out, _, _ := runCLI(t, "config", "get")
	resolved, _ := filepath.EvalSymlinks(root)
	for _, want := range []string{resolved, "Backup", `"maxDepth": 4`} {
		if !strings.Contains(out, want) {
			t.Fatalf("get missing %q:\n%s", want, out)
		}
	}
	if _, stderr, code := runCLI(t, "config", "set", "mode", "hybrid"); code != 0 {
		t.Fatalf("set mode: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "config", "set-mode", root, "container"); code != 0 {
		t.Fatalf("set-mode: %s", stderr)
	}
	if _, _, code := runCLI(t, "config", "set", "mode", "fast"); code == 0 {
		t.Fatal("bad mode accepted")
	}
	out, _, _ = runCLI(t, "config", "get")
	for _, want := range []string{`"mode": "hybrid"`, `"modes"`, resolved} {
		if !strings.Contains(out, want) {
			t.Fatalf("get missing %q:\n%s", want, out)
		}
	}
	if _, _, code := runCLI(t, "config", "remove-root", root); code != 0 {
		t.Fatal("remove-root failed")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("remove-root touched the disk")
	}
}
