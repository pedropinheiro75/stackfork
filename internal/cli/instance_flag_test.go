package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/testsupport"
)

// worktreeFixture prepares an instance in a linked worktree and returns the
// main checkout, the worktree, the instance name and the state flags.
func worktreeFixture(t *testing.T) (main, wt, instance string, flags []string, log string) {
	t.Helper()
	log = installFakeDocker(t)
	main = initRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", main, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("commit", "-q", "--allow-empty", "-m", "init")
	wt = filepath.Join(t.TempDir(), "wt")
	run("worktree", "add", "-q", "-b", "feat", wt)
	if err := os.WriteFile(filepath.Join(wt, "compose.yaml"), []byte(fixtureHTTPCompose+"  db:\n    image: postgres:17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flags = []string{"--state-dir", filepath.Join(t.TempDir(), "state"), "--legacy-state-dir", t.TempDir()}
	out, stderr, code := runCLI(t, append(flags, "--root", wt, "prepare", "-f", filepath.Join(wt, "compose.yaml"))...)
	if code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	var prepared map[string]interface{}
	if err := json.Unmarshal([]byte(out), &prepared); err != nil {
		t.Fatal(err)
	}
	instance, _ = prepared["project"].(string)
	return
}

func TestInstanceFlagFromAnotherCheckout(t *testing.T) {
	main, _, instance, flags, log := worktreeFixture(t)
	t.Setenv("DOCKER_FAKE_STATUS", `{"ID":"c1","Name":"web-1","Service":"web","State":"running","Health":"","Publishers":[]}`+"\n")

	cases := [][]string{
		{"restart", "db"}, {"start", "db"}, {"stop", "db"}, {"status"}, {"urls"},
		{"logs", "db"}, {"exec", "db", "true"}, {"run", "db", "true"}, {"up"}, {"down"},
	}
	for _, c := range cases {
		os.Truncate(log, 0)
		args := append(append([]string{}, flags...), "--root", main)
		args = append(args, c...)
		args = append(args, "--instance", instance) // global flag works after the command
		if _, stderr, code := runCLI(t, args...); code != 0 {
			t.Fatalf("%v --instance failed: code=%d stderr=%s", c, code, stderr)
		}
	}

	// The same instance via a flag placed before the command.
	if _, stderr, code := runCLI(t, append(append([]string{}, flags...), "--root", main, "--instance", instance, "restart", "db")...); code != 0 {
		t.Fatalf("restart with leading --instance: %s", stderr)
	}
	if !strings.Contains(readLog(t, log), "restart db") {
		t.Fatalf("expected docker compose restart db:\n%s", readLog(t, log))
	}
}

func TestInstanceFlagUnknownAndUnsupported(t *testing.T) {
	main, _, _, flags, _ := worktreeFixture(t)
	_, stderr, code := runCLI(t, append(append([]string{}, flags...), "--root", main, "status", "--instance", "sf-nope-123")...)
	if code == 0 || !strings.Contains(stderr, "no such instance") {
		t.Fatalf("expected no such instance, got code=%d %q", code, stderr)
	}
	_, stderr, code = runCLI(t, append(append([]string{}, flags...), "--root", main, "where", "--instance", "sf-nope-123")...)
	if code == 0 || !strings.Contains(stderr, "not supported") {
		t.Fatalf("expected unsupported, got code=%d %q", code, stderr)
	}
}

func TestNoStackErrorListsWorktreeInstances(t *testing.T) {
	main, wt, instance, flags, _ := worktreeFixture(t)
	_, stderr, code := runCLI(t, append(append([]string{}, flags...), "--root", main, "status")...)
	if code == 0 {
		t.Fatal("expected failure in a checkout without a stack")
	}
	for _, want := range []string{"No prepared stack for this checkout/branch", instance, "cd ", "--instance " + instance} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("error missing %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stderr, filepath.Base(wt)) {
		t.Fatalf("error should name the worktree path:\n%s", stderr)
	}
}

func TestInstanceNameGivenAsService(t *testing.T) {
	main, _, instance, flags, _ := worktreeFixture(t)
	for _, action := range []string{"restart", "start", "stop", "logs"} {
		_, stderr, code := runCLI(t, append(append([]string{}, flags...), "--root", main, action, instance)...)
		if code == 0 || !strings.Contains(stderr, "is an instance name, not a service") || !strings.Contains(stderr, "--instance "+instance) {
			t.Fatalf("%s: expected the instance-name error, got code=%d %q", action, code, stderr)
		}
	}
	_, stderr, code := runCLI(t, append(append([]string{}, flags...), "--root", main, "prepare", instance)...)
	if code == 0 || !strings.Contains(stderr, "is an instance name") {
		t.Fatalf("prepare: expected the instance-name error, got %q", stderr)
	}
}

func TestOpenPrintAndLaunch(t *testing.T) {
	main, _, instance, flags, _ := worktreeFixture(t)
	ps := `{"Labels":"io.stackfork.root=/whatever,com.docker.compose.project=` + instance +
		`,com.docker.compose.service=web","State":"running","Status":"","Ports":"127.0.0.1:54321->8080/tcp"}`
	t.Setenv("DOCKER_FAKE_PS", ps+"\n")
	t.Setenv("DOCKER_FAKE_STATUS", `{"ID":"c1","Name":"web-1","Service":"web","State":"running","Health":"","Publishers":[{"Protocol":"tcp","PublishedPort":54321,"TargetPort":8080,"URL":"127.0.0.1"}]}`+"\n")
	base := append(append([]string{}, flags...), "--root", main, "--instance", instance)

	out, stderr, code := runCLI(t, append(base, "open", "--print")...)
	if code != 0 {
		t.Fatalf("open --print: code=%d %s", code, stderr)
	}
	if !strings.HasPrefix(out, "http://") || !strings.Contains(out, ":54321") {
		t.Fatalf("expected the direct app URL, got %q", out)
	}
	if o2, _, code := runCLI(t, append(base, "open", "web", "--print")...); code != 0 || o2 != out {
		t.Fatalf("open web --print: code=%d %q", code, o2)
	}
	if _, stderr, code := runCLI(t, append(base, "open", "db", "--print")...); code == 0 || !strings.Contains(stderr, "no HTTP URL") {
		t.Fatalf("a service with no URL must be refused: %q", stderr)
	}

	// Without --print the opener runs: a fake `open`/`xdg-open`/`rundll32` records the URL.
	dir := t.TempDir()
	rec := filepath.Join(dir, "opened")
	script := "#!/bin/sh\necho \"$*\" >> '" + filepath.ToSlash(rec) + "'\n"
	for _, name := range []string{"open", "xdg-open", "rundll32"} {
		testsupport.FakeExe(t, filepath.Join(dir, name), script)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, stderr, code := runCLI(t, append(base, "open")...); code != 0 {
		t.Fatalf("open: %s", stderr)
	}
	got, _ := os.ReadFile(rec)
	if !strings.Contains(string(got), ":54321") {
		t.Fatalf("the opener should receive the URL, got %q", got)
	}
}

func TestOpenRefusesStoppedInstance(t *testing.T) {
	main, _, instance, flags, _ := worktreeFixture(t)
	_, stderr, code := runCLI(t, append(append([]string{}, flags...), "--root", main, "--instance", instance, "open", "--print")...)
	if code == 0 || !strings.Contains(stderr, "not running") {
		t.Fatalf("expected a not-running refusal, got code=%d %q", code, stderr)
	}
}
