package cli

import (
	"os"
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
