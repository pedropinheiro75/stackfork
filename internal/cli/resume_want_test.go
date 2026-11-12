package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
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

// start-env's own up names the services a plan's exclude leaves (hybrid
// does this too); it is still a whole start and records want_running, and
// the plan stopping the excluded service does not clear it.
func TestStartEnvWithAnExcludeSetsWantRunning(t *testing.T) {
	flog := installFakeDocker(t)
	root := initRepo(t)
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  db:\n    image: postgres:17\n  app:\n    image: busybox\n"), 0o600)
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, NoSeed: true,
		Steps: []knowledge.Step{{ID: "prep", Kind: knowledge.KindPrepare, Args: []string{"-f", "compose.yaml"}}, {ID: "up", Kind: knowledge.KindComposeUp, Exclude: []string{"app"}}}}}
	if err := verifiedFixture(k).Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	if out, stderr, code := runCLI(t, "--state-dir", stateDir, "start-env", root); code != 0 {
		t.Fatalf("start-env: %s %s", out, stderr)
	}
	ms, _ := filepath.Glob(filepath.Join(stateDir, "*", "manifest.json"))
	if len(ms) != 1 || !wantRunning(t, filepath.Dir(ms[0])) {
		t.Fatalf("start-env must set want_running (%v)\n%s", ms, fakeLog(flog))
	}
}
