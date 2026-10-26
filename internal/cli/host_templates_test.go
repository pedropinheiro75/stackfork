package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
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
		port("studio"), port("api"), "20050", "http://127.0.0.1:20050", host}
	for i, w := range want {
		if i < 6 && argv[i+1] != w || i == 8 && argv[i+1] != w {
			t.Errorf("arg %d: got %q want %q", i+1, argv[i+1], w)
		}
	}
	if !strings.Contains(argv[7], "20050") || !strings.Contains(argv[8], "20050") {
		t.Errorf("service values: %q", argv[7:9])
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
	for _, ph := range []string{"{{nope}}", "{{ghost.port}}", "{{service.cache.url}}"} {
		out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "app", "--", os.Args[0], "x", ph)
		if code == 0 || !strings.Contains(stderr, ph) || strings.Contains(out, "argv=") {
			t.Errorf("%s: code %d stdout %q stderr %q", ph, code, out, stderr)
		}
	}
}

func TestHostStartRefusesUnknownPlaceholderBeforeSpawning(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	_, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "start", "app", "--", os.Args[0], "{{nope}}")
	if code == 0 || !strings.Contains(stderr, "{{nope}}") {
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
	before := hash(root1)
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
	if hash(root1) != before || hash(root2) != before {
		t.Fatal("the trust hash changed with the checkout or after expansion")
	}
}
