package cli

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestPlanExpandShowsValuesForThisCheckout(t *testing.T) {
	root, stateDir, project := templateEnv(t)
	writeKnowledgeJSON(t, root, `{"schema":2,"plan":{"status":"recorded","steps":[{"id":"up","kind":"compose-up"},
{"id":"api","kind":"host","name":"api","cmd":"node api --port {{port}}"},
{"id":"app","kind":"host","name":"app","cmd":"vite --port {{port}} --api {{api.url}}"}]}}`)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "api", "--", os.Args[0], "{{app.port}}"); code != 0 {
		t.Fatal(stderr) // allocates the ports of the plan
	}
	m := readManifestAt(t, stateDir, project)
	plain, _, _ := runCLI(t, "--root", root, "--state-dir", stateDir, "plan", "--mode", "hybrid")
	if !strings.Contains(plain, "{{api.url}}") || strings.Contains(plain, strconv.Itoa(m.HostPorts["api"])) {
		t.Fatalf("plain plan:\n%s", plain)
	}
	exp, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "plan", "--mode", "hybrid", "--expand")
	if code != 0 || !strings.Contains(exp, "{{api.url}}") || !strings.Contains(exp, ":"+strconv.Itoa(m.HostPorts["api"])) || !strings.Contains(exp, "--port "+strconv.Itoa(m.HostPorts["app"])) {
		t.Fatalf("code %d %s\n%s", code, stderr, exp)
	}
	js, _, _ := runCLI(t, "--root", root, "--state-dir", stateDir, "plan", "--mode", "hybrid", "--json")
	if !strings.Contains(js, `"expanded"`) || !strings.Contains(js, "--port "+strconv.Itoa(m.HostPorts["app"])) {
		t.Fatalf("json:\n%s", js)
	}
}

func TestPlanExpandUsesProxyURLWhenProxyRuns(t *testing.T) {
	root, stateDir, _ := templateEnv(t)
	writeKnowledgeJSON(t, root, `{"schema":2,"plan":{"status":"recorded","steps":[{"id":"up","kind":"compose-up"},
{"id":"api","kind":"host","name":"api","cmd":"env UI_URL={{app.url}} node api --port {{port}}"},
{"id":"app","kind":"host","name":"app","cmd":"env API={{api.url}} vite --port {{port}}"}]}}`)
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "host", "api", "--", os.Args[0], "{{app.port}}"); code != 0 {
		t.Fatal(stderr)
	}
	expand := func() string {
		out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "plan", "--mode", "hybrid", "--expand")
		if code != 0 {
			t.Fatal(stderr)
		}
		return out
	}
	if out := expand(); !regexp.MustCompile(`UI_URL=http://[a-z0-9.-]+\.localhost:\d+ `).MatchString(out) {
		t.Fatalf("proxy off keeps the port form:\n%s", out)
	}
	t.Setenv("DOCKER_FAKE_PROXY_RUNNING", "true")
	out := expand()
	if !regexp.MustCompile(`UI_URL=http://[a-z0-9.-]+\.localhost `).MatchString(out) || !regexp.MustCompile(`API=http://[a-z0-9.-]+\.localhost:\d+ `).MatchString(out) {
		t.Fatalf("proxy on: the served app is portless, the api keeps its port:\n%s", out)
	}
}
