package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusHintsRestartForUnhealthyOrExited(t *testing.T) {
	root, stateDir, _, _ := envSetup(t, envFixturePlaceholder)
	t.Setenv("DOCKER_FAKE_STATUS",
		`{"ID":"c1","Name":"db-1","Service":"db","State":"running","Health":"unhealthy","Publishers":[]}`+"\n"+
			`{"ID":"c2","Name":"web-1","Service":"web","State":"exited","Health":"","Publishers":[]}`+"\n"+
			`{"ID":"c3","Name":"ok-1","Service":"ok","State":"running","Health":"healthy","Publishers":[]}`+"\n")
	out, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "status")
	if code != 0 {
		t.Fatalf("status failed: %s", stderr)
	}
	for _, want := range []string{"try `stackfork restart db`", "try `stackfork restart web`"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "restart ok") {
		t.Errorf("a healthy service must get no hint:\n%s", stderr)
	}
	hints := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var row struct {
			Service string `json:"Service"`
			Hint    string `json:"hint"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("status line is not JSON: %q", line)
		}
		hints[row.Service] = row.Hint
	}
	if !strings.Contains(hints["db"], "stackfork restart db") || !strings.Contains(hints["web"], "stackfork restart web") || hints["ok"] != "" {
		t.Fatalf("JSON hints: %v", hints)
	}
	if strings.Contains(out, `"ok"`) && strings.Contains(out, `"hint":""`) {
		t.Fatalf("an empty hint must be omitted:\n%s", out)
	}
}

func TestInventoryWarnsWithRestartHint(t *testing.T) {
	root, stateDir, project, _ := envSetup(t, envFixturePlaceholder)
	_ = root
	ps := `{"Labels":"io.stackfork.root=/whatever,com.docker.compose.project=` + project +
		`,com.docker.compose.service=db","State":"running","Status":"Up 2 minutes (unhealthy)","Ports":"127.0.0.1:20050->5432/tcp"}`
	t.Setenv("DOCKER_FAKE_PS", ps+"\n")
	out, stderr, code := runCLI(t, "--state-dir", stateDir, "--legacy-state-dir", t.TempDir(), "list", "--json")
	if code != 0 {
		t.Fatalf("list --json: %s", stderr)
	}
	if !strings.Contains(out, "try `stackfork restart db`") {
		t.Fatalf("list --json (and so the dashboard) should carry the hint:\n%s", out)
	}
}
