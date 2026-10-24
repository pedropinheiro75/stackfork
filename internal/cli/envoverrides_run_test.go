package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func TestApplyEnvRecordsOneStepPerContainerService(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	writeOverrides(t, dir, "web", "ENABLE_GUEST_ACCESS", "true")
	t.Setenv("DOCKER_FAKE_PS", project+"\n") // web runs
	os.Truncate(log, 0)
	code, err := lifecycle.ApplyEnv(io.Discard, "cli", root, envBranch, project, dir, stateDir, "", []string{"web"})
	if code != 0 || err != nil {
		t.Fatalf("apply: %d %v", code, err)
	}
	r := runs.Latest(dir)
	if r == nil || r.Command != "env" || r.Trigger != "cli" || r.Outcome != runs.Succeeded || len(r.Steps) != 1 {
		t.Fatalf("run: %+v", r)
	}
	if s := r.Steps[0]; s.ID != "recreate-web" || s.Kind != "env" || s.State != runs.Succeeded || s.Text != "recreate web" {
		t.Errorf("step: %+v", s)
	}
}

func TestApplyEnvRunLogIsRedacted(t *testing.T) {
	_, root, stateDir, project, dir := envFixture(t)
	writeOverrides(t, dir, "web", "API_TOKEN", "tok-0123456789abcdef")
	t.Setenv("DOCKER_FAKE_PS", project+"\n")
	t.Setenv("DOCKER_FAKE_UP_MSG", "container crashed with tok-0123456789abcdef in its env")
	var out strings.Builder
	code, err := lifecycle.ApplyEnv(&out, "cli", root, envBranch, project, dir, stateDir, "", []string{"web"})
	if code == 0 || err != nil {
		t.Fatalf("a failed apply returns its code: %d %v", code, err)
	}
	r := runs.Latest(dir)
	if r.Outcome != runs.Failed || !strings.Contains(r.Error, "env unset") || strings.Contains(r.Error, "tok-0123456789abcdef") {
		t.Fatalf("run: %+v", r)
	}
	logb, err := runs.Log(dir, r.ID, r.Steps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logb), "tok-0123456789abcdef") || strings.Contains(out.String(), "tok-0123456789abcdef") {
		t.Fatalf("secret leaked:\nlog=%s\nout=%s", logb, out.String())
	}
	if !strings.Contains(string(logb), "[redacted:") {
		t.Errorf("expected a redaction marker: %s", logb)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 1 {
		t.Error("the file must stay after a failed apply")
	}
}

func TestApplyEnvHostAppSteps(t *testing.T) {
	_, root, stateDir, project, dir := envFixture(t)
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	m.HostProcs = map[string]state.HostProc{"app": {PID: 1}, "idle": {PID: 2}}
	state.WritePrivate(filepath.Join(dir, "manifest.json"), m)
	writeOverrides(t, dir, "app", "FLAG", "1", "idle", "FLAG", "1")

	var restarted []string
	oldRestart := lifecycle.HostRestart
	lifecycle.HostRestart = func(_ io.Writer, _, _, name, _, _ string) error { restarted = append(restarted, name); return nil }
	oldRunning := lifecycle.SetHostRunning(func(p state.HostProc) bool { return p.PID == 1 })
	defer func() { lifecycle.HostRestart = oldRestart; lifecycle.SetHostRunning(oldRunning) }()

	var out strings.Builder
	code, err := lifecycle.ApplyEnv(&out, "cli", root, envBranch, project, dir, stateDir, "", []string{"app", "idle"})
	if code != 0 || err != nil {
		t.Fatalf("apply: %d %v", code, err)
	}
	if strings.Join(restarted, ",") != "app" {
		t.Errorf("restarted %v", restarted)
	}
	r := runs.Latest(dir)
	if len(r.Steps) != 2 || r.Steps[0].ID != "restart-app" || r.Steps[0].State != runs.Succeeded ||
		r.Steps[1].State != runs.Skipped || r.Steps[1].Reason != "not running; applies at next start" {
		t.Fatalf("steps: %+v %+v", r.Steps[0], r.Steps[1])
	}
	if !strings.Contains(out.String(), "not running; applies at next start") {
		t.Errorf("output: %s", out.String())
	}
}

func TestApplyEnvDoesNotStartAStoppedService(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	writeOverrides(t, dir, "web", "A", "1")
	os.Truncate(log, 0) // DOCKER_FAKE_PS is empty: nothing runs
	var out strings.Builder
	if code, err := lifecycle.ApplyEnv(&out, "cli", root, envBranch, project, dir, stateDir, "", []string{"web"}); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	if strings.Contains(fakeLog(log), " up ") {
		t.Errorf("a stopped service must not be started:\n%s", fakeLog(log))
	}
	r := runs.Latest(dir)
	if r.Steps[0].State != runs.Skipped || r.Steps[0].Reason != "not running; applies at next up" {
		t.Errorf("step: %+v", r.Steps[0])
	}
}
