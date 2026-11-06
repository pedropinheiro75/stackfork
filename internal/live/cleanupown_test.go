package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// recordManifest writes the instance's manifest with the compose file and the
// env file that prepare recorded (absolute paths in the worktree).
func recordManifest(t *testing.T, f *cleanupFx, files []string, env string) {
	t.Helper()
	m := state.Manifest{Project: f.Inst.Project, Root: f.WT, Branch: "feature-login"}
	for _, x := range files {
		m.Files = append(m.Files, filepath.Join(f.WT, x))
	}
	if env != "" {
		e := filepath.Join(f.WT, env)
		m.EnvFile = &e
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(f.Inst.InstanceDir, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func excludeInMain(t *testing.T, f *cleanupFx, line string) {
	t.Helper()
	p := filepath.Join(f.Main, ".git", "info", "exclude")
	os.MkdirAll(filepath.Dir(p), 0o755)
	fh, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fh.WriteString(line + "\n")
	fh.Close()
}

// The runtime env and the compose file that prepare recorded for the instance
// are stackfork's own and do not block `auto`; a person's file in the same
// folder does, and then only the ignored files are what blocks.
func TestScanCleanupAutoIgnoresTheFilesTheManifestRecords(t *testing.T) {
	f := newCleanupFx(t)
	excludeInMain(t, f, "/.agent-dev")
	for _, root := range []string{f.Main, f.WT} {
		write(t, root, ".agent-dev/nuxt.compose.yaml", "services: {}\n")
		write(t, root, ".agent-dev/runtime.env", "A=1\n")
	}
	recordManifest(t, f, []string{".agent-dev/nuxt.compose.yaml"}, ".agent-dev/runtime.env")
	in := ScanInput{Main: f.Main, NewBase: f.State, Settings: config.Cleanup{Mode: config.CleanupAuto, GraceHours: 1}, Now: time.Now(), Targets: []Target{f.Inst}}
	cs, err := ScanCleanup(in)
	if err != nil || len(cs) != 1 || !cs[0].Eligible || len(cs[0].Ignored) != 0 {
		t.Fatalf("own files only: %+v %v", cs, err)
	}
	write(t, f.WT, ".agent-dev/notes.md", "mine\n")
	cs, _ = ScanCleanup(in)
	c := cs[0]
	if c.Eligible || !c.IgnoredOnly || !slices.Equal(c.Ignored, []string{".agent-dev/notes.md"}) {
		t.Fatalf("a person's file must block auto and be the only entry: %+v", c)
	}
	// the manual path lists the same file and is allowed
	in.Settings.Mode = config.CleanupSuggest
	cs, _ = ScanCleanup(in)
	if !cs[0].Eligible || !slices.Equal(cs[0].Ignored, []string{".agent-dev/notes.md"}) || cs[0].IgnoredOnly {
		t.Fatalf("suggest: %+v", cs[0])
	}
}

// A file the manifest records but the main checkout does not have is not
// known to be regenerable, so it keeps blocking.
func TestOwnFilesNeedACopyInTheMainCheckout(t *testing.T) {
	f := newCleanupFx(t)
	write(t, f.Main, ".agent-dev/a.yaml", "a\n")
	write(t, f.WT, ".agent-dev/a.yaml", "a\n")
	write(t, f.WT, ".agent-dev/only-here.yaml", "b\n")
	recordManifest(t, f, []string{".agent-dev/a.yaml", ".agent-dev/only-here.yaml", "../outside.yaml"}, "")
	if got, want := ownFiles(f.Main, &f.Inst), []string{".agent-dev/a.yaml"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if ownFiles(f.Main, nil) != nil {
		t.Fatal("no instance, no record")
	}
}
