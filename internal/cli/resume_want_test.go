package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

func wantRunning(t *testing.T, dir string) bool {
	t.Helper()
	m, err := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil || m == nil {
		t.Fatalf("manifest: %v", err)
	}
	return m.WantRunning
}

func TestWantRunningSetByUpClearedByDown(t *testing.T) {
	_, root, stateDir, _, dir := forgetFixture(t)
	if wantRunning(t, dir) {
		t.Fatal("a prepared instance does not want to run yet")
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code != 0 {
		t.Fatalf("up: code=%d %s", code, stderr)
	}
	if !wantRunning(t, dir) {
		t.Fatal("up must set want_running")
	}
	// a re-prepare keeps the person's last word
	composePath := filepath.Join(root, "compose.yaml")
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "prepare", "-f", composePath); code != 0 {
		t.Fatalf("prepare: code=%d %s", code, stderr)
	}
	if !wantRunning(t, dir) {
		t.Fatal("prepare must keep want_running")
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "down"); code != 0 {
		t.Fatalf("down: code=%d %s", code, stderr)
	}
	if wantRunning(t, dir) {
		t.Fatal("down must clear want_running")
	}
}

func TestWantRunningStaysWhenUpFails(t *testing.T) {
	_, root, stateDir, _, dir := forgetFixture(t)
	t.Setenv("DOCKER_FAKE_EXIT", "1")
	if _, _, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code == 0 {
		t.Fatal("expected a failure")
	}
	if wantRunning(t, dir) {
		t.Fatal("a failed up must not set want_running")
	}
}

func TestResumeCommandIsAnAction(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	out, _, code := runCLI(t, "--state-dir", stateDir, "resume", "--help")
	if code != 0 || !strings.Contains(out, "resume") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestConfigSetResumeOnStart(t *testing.T) {
	var out strings.Builder
	if code, err := cmdConfig(&out, []string{"set", "resume-on-start", "off"}); code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	out.Reset()
	cmdConfig(&out, []string{"get"})
	if !strings.Contains(out.String(), `"resumeOnStart": false`) {
		t.Fatalf("get: %s", out.String())
	}
	if code, err := cmdConfig(&out, []string{"set", "resume-on-start", "maybe"}); err == nil {
		t.Fatalf("code=%d: want an error for maybe", code)
	}
	cmdConfig(&out, []string{"set", "resume-on-start", "on"})
	out.Reset()
	cmdConfig(&out, []string{"get"})
	if !strings.Contains(out.String(), `"resumeOnStart": true`) {
		t.Fatalf("get: %s", out.String())
	}
}

func TestUpOfOneServiceDoesNotSetWantRunning(t *testing.T) {
	_, root, stateDir, _, dir := forgetFixture(t)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up", "--service", "db"); code != 0 {
		t.Fatalf("up --service: %s", stderr)
	}
	if wantRunning(t, dir) {
		t.Fatal("up SERVICE says nothing about the rest")
	}
}
