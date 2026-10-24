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

func TestApplyEnvSkipsAServiceWithNoContainer(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	writeOverrides(t, dir, "web", "A", "1")
	os.Truncate(log, 0) // DOCKER_FAKE_PS and _ALL are empty: no container at all
	var out strings.Builder
	res, code, err := lifecycle.ApplyEnvDetailed(&out, "cli", root, envBranch, project, dir, stateDir, "", []string{"web"})
	if code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	if strings.Contains(fakeLog(log), " up ") {
		t.Errorf("nothing may be created:\n%s", fakeLog(log))
	}
	if len(res) != 1 || res[0].Kind != lifecycle.EnvSkipped || res[0].Detail != "no container" {
		t.Errorf("outcome %+v", res)
	}
	r := runs.Latest(dir)
	if r.Steps[0].State != runs.Skipped || r.Steps[0].Reason != "no container; applies at next up" {
		t.Errorf("step: %+v", r.Steps[0])
	}
}

// A stopped container is recreated without starting it: start and restart
// would reuse the old container and its old environment.
func TestApplyEnvRecreatesAStoppedContainerWithoutStartingIt(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	writeOverrides(t, dir, "web", "A", "1")
	t.Setenv("DOCKER_FAKE_PS_ALL", project+"\n") // exists; `docker ps` (running only) stays empty
	os.Truncate(log, 0)
	res, code, err := lifecycle.ApplyEnvDetailed(io.Discard, "cli", root, envBranch, project, dir, stateDir, "", []string{"web"})
	if code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	calls := upCalls(log)
	if len(calls) != 1 || !strings.HasSuffix(strings.TrimSpace(calls[0]), " up -d --no-deps --no-start web") || strings.Contains(calls[0], "--wait") {
		t.Fatalf("up calls %q", calls)
	}
	if len(res) != 1 || res[0].Kind != lifecycle.EnvRecreatedStopped {
		t.Errorf("outcome %+v", res)
	}
	if lifecycle.DescribeEnvOutcomes(res) != "recreated (stopped): web" {
		t.Errorf("message %q", lifecycle.DescribeEnvOutcomes(res))
	}
}

func TestDescribeEnvOutcomes(t *testing.T) {
	got := lifecycle.DescribeEnvOutcomes([]lifecycle.EnvOutcome{
		{Service: "web", Kind: lifecycle.EnvApplied}, {Service: "db", Kind: lifecycle.EnvRecreatedStopped},
		{Service: "x", Kind: lifecycle.EnvSkipped, Detail: "no container"}})
	if got != "applied: web; recreated (stopped): db; skipped: x (no container)" {
		t.Errorf("%q", got)
	}
}

// The dashboard has no shell environment: an env: reference it cannot resolve
// is served from the values it checked earlier (add-env-overrides I4).
func TestApplyEnvUsesRememberedSecretValues(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	t.Setenv("DOCKER_FAKE_PS", project+"\n")
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	m.Secrets = map[string]string{"my_cred": "env:STACKFORK_TEST_CRED_NOT_SET"}
	state.WritePrivate(filepath.Join(dir, "manifest.json"), m)
	writeOverrides(t, dir, "web", "A", "1")
	os.Unsetenv("STACKFORK_TEST_CRED_NOT_SET")

	if _, code, err := lifecycle.ApplyEnvDetailed(io.Discard, "dashboard", root, envBranch, project, dir, stateDir, "", []string{"web"}); code == 0 && err == nil {
		t.Fatal("without a remembered value the apply must fail on the unresolved reference")
	}
	lifecycle.CacheSecrets(dir, lifecycle.SecretValues{"my_cred": {Ref: "env:STACKFORK_TEST_CRED_NOT_SET", Value: "remembered-value-1234"}})
	os.Truncate(log, 0)
	var out strings.Builder
	if _, code, err := lifecycle.ApplyEnvDetailed(&out, "dashboard", root, envBranch, project, dir, stateDir, "", []string{"web"}); code != 0 || err != nil {
		t.Fatalf("%d %v %s", code, err, out.String())
	}
	if !strings.Contains(dockerEnvLog(log), "my_cred=remembered-value-1234") || strings.Contains(out.String(), "remembered-value-1234") {
		t.Errorf("the child must get the value and the output must not:\n%s", out.String())
	}
}

// Apply retries once after a stale network endpoint, like up (M5).
func TestApplyEnvRetriesAfterStaleEndpoint(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	t.Setenv("DOCKER_FAKE_PS", project+"\n")
	t.Setenv("DOCKER_FAKE_NETWORKS", project+"_default\n")
	t.Setenv("DOCKER_FAKE_UP_MSG", "Error response from daemon: endpoint with name "+project+"-web-1 already exists in network "+project+"_default")
	writeOverrides(t, dir, "web", "A", "1")
	os.Truncate(log, 0)
	if _, code, err := lifecycle.ApplyEnvDetailed(io.Discard, "cli", root, envBranch, project, dir, stateDir, "", []string{"web"}); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	if strings.Count(fakeLog(log), " --no-deps --wait ") != 2 || !strings.Contains(fakeLog(log), "network disconnect -f "+project+"_default "+project+"-web-1") {
		t.Fatalf("expected a disconnect and one retry:\n%s", fakeLog(log))
	}
}
