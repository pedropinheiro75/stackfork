package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

const hybridPlanJSON = `{"schema":2,"plan":{"status":"recorded","steps":[
{"id":"up","kind":"compose-up"},
{"id":"api","kind":"host","name":"api","cmd":"echo api"},
{"id":"app","kind":"host","name":"app","cmd":"echo app"},
{"id":"studio","kind":"host","name":"studio","cmd":"echo studio"}]}}`

// templateEnv is a running database-only instance whose plan has the host
// apps api, app and studio.
func templateEnv(t *testing.T) (root, stateDir, project string) {
	t.Helper()
	t.Setenv("STACKFORK_PORT_RANGE_START", "31500")
	t.Setenv("STACKFORK_PORT_RANGE_END", "31999")
	t.Setenv("STACKFORK_TEST_HELPER", "1")
	root, stateDir, project, psLine := envSetup(t, hostFixture)
	t.Setenv("DOCKER_FAKE_PS", psLine+"\n")
	writeKnowledgeJSON(t, root, hybridPlanJSON)
	return
}

func helperArgv(t *testing.T, out string) []string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "argv="); ok {
			var a []string
			if err := json.Unmarshal([]byte(v), &a); err != nil {
				t.Fatal(err)
			}
			return a
		}
	}
	t.Fatalf("the command did not run:\n%s", out)
	return nil
}

func TestHostExpandsEveryPlaceholder(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", os.Args[0],
		"env", "UI_URL={{url}}", "API={{api.url}}", "--port", "{{port}}", "{{studio.port}}", "{{api.port}}",
		"{{service.db.port}}", "{{service.db.url}}", "{{hostname}}")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	m := readManifestAt(t, stateDir, project)
	port := func(n string) string { return strconv.Itoa(m.HostPorts[n]) }
	host := project[strings.Index(project, "-")+1:] + ".localhost"
	argv := helperArgv(t, out)
	if len(argv) != 10 || argv[0] != "env" {
		t.Fatalf("argv %q", argv)
	}
	want := []string{"UI_URL=http://" + host + ":" + port("app"), "API=http://" + host + ":" + port("api"), "--port", port("app"),
		port("studio"), port("api"), "20050", "127.0.0.1:20050", host}
	for i, w := range want {
		if argv[i+1] != w {
			t.Errorf("arg %d: got %q want %q", i+1, argv[i+1], w)
		}
	}
	// every host app of the plan has its own port, before it starts
	seen := map[int]bool{}
	for _, n := range []string{"api", "app", "studio"} {
		if m.HostPorts[n] == 0 || seen[m.HostPorts[n]] {
			t.Fatalf("host_ports %v", m.HostPorts)
		}
		seen[m.HostPorts[n]] = true
	}
	if !strings.Contains(out, "PORT="+port("app")) {
		t.Errorf("PORT is not the app port:\n%s", out)
	}
}

func TestHostUnknownPlaceholderRunsNothing(t *testing.T) {
	root, stateDir, _ := templateEnv(t)
	for _, ph := range []string{"{{ghost.url}}", "{{ghost.port}}", "{{service.cache.url}}"} {
		out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", os.Args[0], "x", ph)
		if code == 0 || !strings.Contains(stderr, ph) || strings.Contains(out, "argv=") {
			t.Errorf("%s: code %d stdout %q stderr %q", ph, code, out, stderr)
		}
	}
}

func TestHostStartRefusesUnknownPlaceholderBeforeSpawning(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "start", "app", "--", os.Args[0], "{{ghost.url}}")
	if code == 0 || !strings.Contains(stderr, "{{ghost.url}}") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if m := readManifestAt(t, stateDir, project); len(m.HostProcs) != 0 {
		t.Fatalf("a supervisor was recorded: %v", m.HostProcs)
	}
}

func TestHostExecExpandsAndHasNoSelf(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	run := func(cmd string) (string, error) {
		var out bytes.Buffer
		_, err := lifecycle.HostExec(&out, project, root, filepath.Join(stateDir, project), stateDir, t.TempDir(),
			knowledge.Step{ID: "studio", Kind: knowledge.KindExec, On: knowledge.OnHost, Cmd: os.Args[0] + " " + cmd})
		return out.String(), err
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", os.Args[0], "{{studio.port}}"); code != 0 {
		t.Fatal(stderr) // gives every host app of the plan its port
	}
	out, err := run("--port {{studio.port}} {{app.url}} {{service.db.port}}")
	if err != nil {
		t.Fatal(err)
	}
	m := readManifestAt(t, stateDir, project)
	argv := helperArgv(t, out)
	if argv[1] != strconv.Itoa(m.HostPorts["studio"]) || !strings.HasSuffix(argv[2], ":"+strconv.Itoa(m.HostPorts["app"])) || argv[3] != "20050" {
		t.Fatalf("argv %q host_ports %v", argv, m.HostPorts)
	}
	if out, err := run("{{port}}"); err == nil || !strings.Contains(err.Error(), "{{port}}") || strings.Contains(out, "argv=") {
		t.Fatalf("{{port}} in an exec step: %v\n%s", err, out)
	}
}

// The same plan text gives two checkouts their own ports and hostnames, and
// expanding never changes what was trusted.
func TestTwoInstancesSamePlanDifferentValuesSameTrust(t *testing.T) {
	root1, stateDir, project1 := templateEnv(t)
	t.Setenv("DOCKER_FAKE_PS", "")
	root2 := initRepo(t)
	composePath := filepath.Join(root2, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(hostFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	pr, stderr, code := runCLI(t, "--root", root2, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatalf("prepare: %s", stderr)
	}
	var prepared map[string]interface{}
	json.Unmarshal([]byte(pr), &prepared)
	project2, _ := prepared["project"].(string)
	if _, stderr, code := runCLI(t, "--root", root2, "--state-dir", stateDir, "up", "--wait-timeout", "5"); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	t.Setenv("DOCKER_FAKE_PS", envPSLine(project1, "db", "20050")+"\n"+envPSLine(project2, "db", "20051")+"\n")
	writeKnowledgeJSON(t, root2, hybridPlanJSON)

	hash := func(root string) string {
		k, err := knowledge.Load(filepath.Join(root, ".stackfork", "knowledge.json"))
		if err != nil {
			t.Fatal(err)
		}
		return knowledge.PlanHash(k.Plan.Steps)
	}
	file := func(root string) string {
		b, _ := os.ReadFile(filepath.Join(root, ".stackfork", "knowledge.json"))
		return string(b)
	}
	before := file(root1)
	cmd := []string{"host", "app", "--", os.Args[0], "{{url}}", "{{api.port}}"}
	o1, e1, c1 := runCLI(t, append([]string{"--root", root1, "--state-dir", stateDir}, cmd...)...)
	o2, e2, c2 := runCLI(t, append([]string{"--root", root2, "--state-dir", stateDir}, cmd...)...)
	if c1 != 0 || c2 != 0 {
		t.Fatalf("host: %d %s / %d %s", c1, e1, c2, e2)
	}
	a1, a2 := helperArgv(t, o1), helperArgv(t, o2)
	if a1[0] == a2[0] || a1[1] == a2[1] {
		t.Fatalf("both instances got %q and %q", a1, a2)
	}
	if hash(root1) != hash(root2) || file(root1) != before {
		t.Fatal("one plan text must hash the same in both checkouts, and expansion must not rewrite it")
	}
}

// {{port}} is the port the route will use: the flag, else the remembered or
// allocated one, never the plan's literal port: when the route ignores it.
func TestHostSelfPortIsTheRoutePort(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	writeKnowledgeJSON(t, root, `{"schema":2,"plan":{"status":"recorded","steps":[{"id":"up","kind":"compose-up"},
{"id":"app","kind":"host","name":"app","port":13999,"cmd":"echo app"}]}}`)
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", os.Args[0], "{{port}}")
	if code != 0 {
		t.Fatal(stderr)
	}
	m := readManifestAt(t, stateDir, project)
	if argv := helperArgv(t, out); argv[0] != strconv.Itoa(m.HostPorts["app"]) || m.HostPorts["app"] == 13999 || !strings.Contains(out, "PORT="+argv[0]) {
		t.Fatalf("argv %q host_ports %v\n%s", argv, m.HostPorts, out)
	}
	p := strconv.Itoa(freePort(t))
	out, stderr, code = runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--port", p, "--", os.Args[0], "{{port}}")
	if code != 0 || helperArgv(t, out)[0] != p {
		t.Fatalf("--port: code %d %s\n%s", code, stderr, out)
	}
}

// A host exec step runs under the instance lock (up, seed): it must not take
// it again. An app with no port yet is an error, not a hang.
func TestHostExecWithPlaceholderDoesNotTakeTheLock(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	instanceDir := filepath.Join(stateDir, project)
	lock, err := state.AcquireLock(filepath.Join(instanceDir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	run := func(cmd string) error {
		done := make(chan error, 1)
		go func() {
			var out bytes.Buffer
			_, err := lifecycle.HostExec(&out, project, root, instanceDir, stateDir, t.TempDir(),
				knowledge.Step{ID: "m", Kind: knowledge.KindExec, On: knowledge.OnHost, Cmd: os.Args[0] + " " + cmd})
			done <- err
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("HostExec hangs on the instance lock")
			return nil
		}
	}
	if err := run("{{app.port}}"); err == nil || !strings.Contains(err.Error(), "app") {
		t.Fatalf("an app with no port must be an error naming it: %v", err)
	}
	if err := run("{{hostname}}"); err != nil {
		t.Fatal(err)
	}
}

// An exec step that uses another tool's template syntax is not touched.
func TestHostExecKeepsForeignTemplates(t *testing.T) {
	_, _, _, run := hostExecEnv(t)
	out, code, err := run(helperStep("", "inspect --format {{.State.Health.Status}}"))
	if err != nil || code != 0 || !strings.Contains(out, `"{{.State.Health.Status}}"`) {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
}

// Ports reserved by another instance for apps that never ran are taken.
func TestHostPortsReservedByOtherInstancesAreTaken(t *testing.T) {
	t.Setenv("STACKFORK_PORT_RANGE_START", "31500")
	t.Setenv("STACKFORK_PORT_RANGE_END", "31507")
	root1, stateDir, project1 := templateEnv(t)
	t.Setenv("STACKFORK_PORT_RANGE_START", "31500")
	t.Setenv("STACKFORK_PORT_RANGE_END", "31507")
	t.Setenv("DOCKER_FAKE_PS", "")
	root2 := initRepo(t)
	composePath := filepath.Join(root2, "compose.yaml")
	os.WriteFile(composePath, []byte(hostFixture), 0o600)
	pr, stderr, code := runCLI(t, "--root", root2, "--state-dir", stateDir, "prepare", "-f", composePath)
	if code != 0 {
		t.Fatal(stderr)
	}
	var prepared map[string]interface{}
	json.Unmarshal([]byte(pr), &prepared)
	project2, _ := prepared["project"].(string)
	if _, stderr, code := runCLI(t, "--root", root2, "--state-dir", stateDir, "up", "--wait-timeout", "5"); code != 0 {
		t.Fatal(stderr)
	}
	t.Setenv("DOCKER_FAKE_PS", envPSLine(project1, "db", "20050")+"\n"+envPSLine(project2, "db", "20051")+"\n")
	writeKnowledgeJSON(t, root2, hybridPlanJSON)
	for _, r := range []string{root1, root2} {
		if _, stderr, code := runCLI(t, "--root", r, "--state-dir", stateDir, "host", "app", "--", os.Args[0], "{{api.port}}", "{{studio.port}}"); code != 0 {
			t.Fatal(stderr)
		}
	}
	seen := map[int]bool{}
	for _, p := range []string{project1, project2} {
		for n, port := range readManifestAt(t, stateDir, p).HostPorts {
			if seen[port] {
				t.Fatalf("port %d of %s is held twice", port, n)
			}
			seen[port] = true
		}
	}
}
