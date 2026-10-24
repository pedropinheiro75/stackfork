package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// envCLI runs `stackfork` against the fixture instance.
func envCLI(t *testing.T, root, stateDir string, args ...string) (string, string, int) {
	t.Helper()
	return runCLI(t, append([]string{"--root", root, "--state-dir", stateDir}, args...)...)
}

func upCalls(log string) []string {
	var out []string
	for _, l := range strings.Split(fakeLog(log), "\n") {
		if strings.HasPrefix(l, "compose ") && strings.Contains(l, " up ") {
			out = append(out, l)
		}
	}
	return out
}

func TestEnvSetStoresAndRecreatesOnlyThatService(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	t.Setenv("DOCKER_FAKE_PS", project+"\n")
	os.Truncate(log, 0)
	out, stderr, code := envCLI(t, root, stateDir, "env", "set", "web", "ENABLE_GUEST_ACCESS=true")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s out=%s", code, stderr, out)
	}
	if !strings.Contains(out, "web ENABLE_GUEST_ACCESS=true") {
		t.Errorf("output: %s", out)
	}
	o, _ := state.ReadEnvOverrides(dir)
	if o.Services["web"]["ENABLE_GUEST_ACCESS"] != "true" {
		t.Errorf("not stored: %+v", o)
	}
	ups := upCalls(log)
	if len(ups) != 1 || !strings.HasSuffix(strings.TrimSpace(ups[0]), " --no-deps --wait --wait-timeout 180 web") {
		t.Fatalf("up calls: %q", ups)
	}
	if r := runs.Latest(dir); r == nil || r.Command != "env" || len(r.Steps) != 1 {
		t.Errorf("run: %+v", r)
	}
}

func TestEnvSetNoRestartAndValueWithEquals(t *testing.T) {
	log, root, stateDir, _, dir := envFixture(t)
	os.Truncate(log, 0)
	if out, stderr, code := envCLI(t, root, stateDir, "env", "set", "web", "URL=http://x?a=b", "--no-restart"); code != 0 {
		t.Fatalf("%d %s %s", code, stderr, out)
	}
	if len(upCalls(log)) != 0 || runs.Latest(dir) != nil {
		t.Errorf("--no-restart must apply nothing:\n%s", fakeLog(log))
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Services["web"]["URL"] != "http://x?a=b" {
		t.Errorf("%+v", o)
	}
}

func TestEnvSetUnknownServiceListsValidNames(t *testing.T) {
	_, root, stateDir, _, dir := envFixture(t)
	_, stderr, code := envCLI(t, root, stateDir, "env", "set", "nope", "FLAG=true")
	if code == 0 || !strings.Contains(stderr, "db, web") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(dir + "/" + state.EnvOverridesFile); !os.IsNotExist(err) {
		t.Error("nothing may be written")
	}
}

func TestEnvSetUsageErrors(t *testing.T) {
	_, root, stateDir, _, _ := envFixture(t)
	for _, args := range [][]string{
		{"env", "set"}, {"env", "set", "web"}, {"env", "set", "web", "NOEQUALS"}, {"env", "set", "web", "1BAD=x"},
		{"env", "set", "web", "A=\nb"}, {"env", "unset", "web"}, {"env", "overrides", "a", "b"},
	} {
		if _, stderr, code := envCLI(t, root, stateDir, args...); code == 0 {
			t.Errorf("%v accepted (stderr=%s)", args, stderr)
		}
	}
}

func TestEnvUnsetRemovesAndRecreates(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	t.Setenv("DOCKER_FAKE_PS", project+"\n")
	writeOverrides(t, dir, "web", "A", "1", "web", "B", "2")
	os.Truncate(log, 0)
	if out, stderr, code := envCLI(t, root, stateDir, "env", "unset", "web", "A"); code != 0 {
		t.Fatalf("%d %s %s", code, stderr, out)
	}
	if o, _ := state.ReadEnvOverrides(dir); strings.Join(o.Pairs("web"), ",") != "B=2" {
		t.Errorf("%+v", o)
	}
	if len(upCalls(log)) != 1 {
		t.Errorf("up calls %q", upCalls(log))
	}
	if _, stderr, code := envCLI(t, root, stateDir, "env", "unset", "web", "A", "--no-restart"); code == 0 || !strings.Contains(stderr, "no override A") {
		t.Errorf("unset of a missing name: %d %s", code, stderr)
	}
}

func TestEnvSecretLikeNamesAreRefusedUnlessAllowed(t *testing.T) {
	_, root, stateDir, _, dir := envFixture(t)
	_, stderr, code := envCLI(t, root, stateDir, "env", "set", "web", "API_TOKEN=abc", "--no-restart")
	if code == 0 || !strings.Contains(stderr, "stackfork secret") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Fatal("written")
	}
	out, stderr, code := envCLI(t, root, stateDir, "env", "set", "web", "API_TOKEN=tok-0123456789abcdef", "--allow-secret", "--no-restart")
	if code != 0 || strings.Contains(out+stderr, "tok-0123456789abcdef") {
		t.Fatalf("code=%d out=%s stderr=%s", code, out, stderr)
	}
	out, _, code = envCLI(t, root, stateDir, "env", "overrides", "--json")
	if code != 0 || strings.Contains(out, "tok-0123456789abcdef") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	var got struct {
		Count    int `json:"count"`
		Services []struct {
			Service   string `json:"service"`
			Kind      string `json:"kind"`
			Overrides []struct {
				Name   string  `json:"name"`
				Value  *string `json:"value"`
				Masked bool    `json:"masked"`
			} `json:"overrides"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Count != 1 || len(got.Services) != 1 || got.Services[0].Service != "web" || got.Services[0].Kind != "container" {
		t.Fatalf("%+v", got)
	}
	e := got.Services[0].Overrides[0]
	if e.Name != "API_TOKEN" || !e.Masked || e.Value != nil || !strings.Contains(out, `"value": null`) {
		t.Errorf("entry %+v\n%s", e, out)
	}
	text, _, _ := envCLI(t, root, stateDir, "env", "overrides")
	if strings.Contains(text, "tok-0123456789abcdef") || !strings.Contains(text, "API_TOKEN") {
		t.Errorf("text: %s", text)
	}
}

func TestEnvOverridesListsAndFilters(t *testing.T) {
	_, root, stateDir, _, dir := envFixture(t)
	writeOverrides(t, dir, "web", "B", "2", "web", "A", "1", "db", "C", "3", "ghost", "D", "4")
	out, _, code := envCLI(t, root, stateDir, "env", "overrides")
	if code != 0 {
		t.Fatal(out)
	}
	for _, want := range []string{"db C=3", "web A=1", "web B=2", "ghost D=4 (unknown service)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Index(out, "web A=1") > strings.Index(out, "web B=2") {
		t.Errorf("not sorted:\n%s", out)
	}
	out, _, _ = envCLI(t, root, stateDir, "env", "overrides", "web")
	if strings.Contains(out, "db C") || !strings.Contains(out, "web A=1") {
		t.Errorf("filter: %s", out)
	}
	out, _, code = envCLI(t, root, stateDir, "env", "overrides", "--json", "web")
	if code != 0 || !strings.Contains(out, `"service": "web"`) || strings.Contains(out, `"db"`) {
		t.Errorf("json filter: %d %s", code, out)
	}
	out, _, code = envCLI(t, root, stateDir, "env", "overrides", "nope")
	if code == 0 && !strings.Contains(out, "no env overrides") {
		t.Errorf("unknown filter: %s", out)
	}
}

func TestEnvOverridesNoneSaysSo(t *testing.T) {
	_, root, stateDir, _, _ := envFixture(t)
	out, _, code := envCLI(t, root, stateDir, "env", "overrides")
	if code != 0 || !strings.Contains(out, "no env overrides") {
		t.Errorf("%d %s", code, out)
	}
}

func TestEnvOverridesClear(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	t.Setenv("DOCKER_FAKE_PS", project+"\n")
	writeOverrides(t, dir, "web", "A", "1", "db", "C", "3")
	os.Truncate(log, 0)
	if out, stderr, code := envCLI(t, root, stateDir, "env", "overrides", "--clear", "web"); code != 0 {
		t.Fatalf("%d %s %s", code, stderr, out)
	}
	o, _ := state.ReadEnvOverrides(dir)
	if o.Count() != 1 || o.Services["db"]["C"] != "3" {
		t.Errorf("%+v", o)
	}
	if ups := upCalls(log); len(ups) != 1 || !strings.HasSuffix(strings.TrimSpace(ups[0]), " web") {
		t.Errorf("ups %q", ups)
	}
	os.Truncate(log, 0)
	if out, stderr, code := envCLI(t, root, stateDir, "env", "overrides", "--clear", "--no-restart"); code != 0 {
		t.Fatalf("%d %s %s", code, stderr, out)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 || len(upCalls(log)) != 0 {
		t.Errorf("clear all: %+v %q", o, upCalls(log))
	}
}

// `env` with no subcommand keeps printing the instance environment block.
func TestEnvWithoutSubcommandIsUnchanged(t *testing.T) {
	root, stateDir, _, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "env")
	if code != 0 || !strings.Contains(out, "export DATABASE_URL=") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	help, _, code := runCLI(t, "env", "--help")
	if code != 0 || !strings.Contains(help, "overrides") || !strings.Contains(help, "-format") {
		t.Errorf("help: %s", help)
	}
}

func TestEnvSetWaitsForABusyInstanceThenApplies(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	t.Setenv("DOCKER_FAKE_PS", project+"\n")
	old := envBusyWait
	envBusyWait = 10 * time.Second
	defer func() { envBusyWait = old }()
	rec, err := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli", Project: project, Branch: envBranch})
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(600 * time.Millisecond); rec.Finish(nil) }()
	os.Truncate(log, 0)
	if out, stderr, code := envCLI(t, root, stateDir, "env", "set", "web", "A=1"); code != 0 {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	if len(upCalls(log)) != 1 {
		t.Errorf("expected the apply after the wait: %q", upCalls(log))
	}
}

func TestEnvSetStillBusyStoresAndSaysSo(t *testing.T) {
	log, root, stateDir, project, dir := envFixture(t)
	old := envBusyWait
	envBusyWait = 300 * time.Millisecond
	defer func() { envBusyWait = old }()
	rec, err := runs.Begin(dir, runs.Meta{Command: "up", Trigger: "cli", Project: project, Branch: envBranch})
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Finish(nil)
	os.Truncate(log, 0)
	_, stderr, code := envCLI(t, root, stateDir, "env", "set", "web", "A=1")
	if code == 0 || !strings.Contains(stderr, "stored, not applied yet: another run is using this instance; it applies at the next `stackfork up` (or run the same `env set` again)") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Services["web"]["A"] != "1" {
		t.Errorf("must stay stored: %+v", o)
	}
	if len(upCalls(log)) != 0 {
		t.Error("nothing may be applied")
	}
}
