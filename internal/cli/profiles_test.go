package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProfileDirsDefault covers task 5.2: with no flag and no env var, every
// directory under $STACKFORK_CONFIG_DIR/profiles is used (symlinks
// resolved), and `where` lists each one's context.md presence and
// reference filenames without reading their content.
func TestProfileDirsDefault(t *testing.T) {
	configDir := t.TempDir()
	profilesRoot := filepath.Join(configDir, "profiles")
	realProfile := filepath.Join(t.TempDir(), "real-profile")
	if err := os.MkdirAll(filepath.Join(realProfile, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realProfile, "context.md"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realProfile, "references", "acme.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(profilesRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realProfile, filepath.Join(profilesRoot, "acme")); err != nil {
		t.Fatal(err)
	}
	// A profile with no context.md must still be listed, with context=false.
	bareProfile := filepath.Join(profilesRoot, "bare")
	if err := os.MkdirAll(bareProfile, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("STACKFORK_CONFIG_DIR", configDir)
	t.Setenv("STACKFORK_PROFILE_DIRS", "")

	dirs, err := resolveProfileDirs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 profile dirs, got %v", dirs)
	}

	infos := profileInfos(dirs)
	byPath := map[string]ProfileInfo{}
	for _, info := range infos {
		byPath[info.Path] = info
	}
	resolvedReal, err := filepath.EvalSymlinks(realProfile)
	if err != nil {
		t.Fatal(err)
	}
	acme, ok := byPath[resolvedReal]
	if !ok {
		t.Fatalf("expected symlinked profile resolved to %s, got %v", resolvedReal, infos)
	}
	if !acme.Context {
		t.Fatalf("expected context=true for acme profile, got %+v", acme)
	}
	if len(acme.References) != 1 || acme.References[0] != "acme.md" {
		t.Fatalf("expected references [acme.md], got %v", acme.References)
	}

	resolvedBare, err := filepath.EvalSymlinks(bareProfile)
	if err != nil {
		t.Fatal(err)
	}
	bare, ok := byPath[resolvedBare]
	if !ok {
		t.Fatalf("expected bare profile listed, got %v", infos)
	}
	if bare.Context {
		t.Fatalf("expected context=false for bare profile, got %+v", bare)
	}
	if len(bare.References) != 0 {
		t.Fatalf("expected no references for bare profile, got %v", bare.References)
	}
}

// TestProfileDirsExplicit covers --profile-dir and STACKFORK_PROFILE_DIRS
// overriding the default scan of $STACKFORK_CONFIG_DIR/profiles.
func TestProfileDirsExplicit(t *testing.T) {
	flagDir := t.TempDir()
	envDir := t.TempDir()

	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir()) // must not be scanned
	t.Setenv("STACKFORK_PROFILE_DIRS", envDir)

	dirs, err := resolveProfileDirs([]string{flagDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected flag dir + env dir, got %v", dirs)
	}
	resolvedFlag, _ := filepath.EvalSymlinks(flagDir)
	resolvedEnv, _ := filepath.EvalSymlinks(envDir)
	if dirs[0] != resolvedFlag || dirs[1] != resolvedEnv {
		t.Fatalf("expected [%s %s], got %v", resolvedFlag, resolvedEnv, dirs)
	}
}

// TestProfileDirsNoneConfigured covers the machine-with-no-profiles case
// (agent-skill spec "Agent on a machine without profiles"): `where` must
// not error, and must report an empty profiles list.
func TestProfileDirsNoneConfigured(t *testing.T) {
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	t.Setenv("STACKFORK_PROFILE_DIRS", "")

	dirs, err := resolveProfileDirs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 {
		t.Fatalf("expected no profile dirs, got %v", dirs)
	}
	if infos := profileInfos(dirs); len(infos) != 0 {
		t.Fatalf("expected no profile infos, got %v", infos)
	}
}
