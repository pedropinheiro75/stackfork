//go:build !windows

package cli

import (
	"strings"
	"testing"
)

const restartPlanJSON = `{"schema":2,"plan":{"modes":["container","hybrid"],"status":"recorded","steps":[
{"id":"up","kind":"compose-up"},
{"id":"app","kind":"host","name":"app","cmd":"sleep 31","modes":["hybrid"]}]}}`

// `host restart` runs the trusted plan's current command, not the recorded one.
func TestHostRestartUsesTheTrustedPlanCommand(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sleep", "30")...); code != 0 {
		t.Fatalf("start: %s", stderr)
	}
	writeKnowledgeJSON(t, root, restartPlanJSON)
	if _, stderr, code := runCLI(t, append(g, "plan", "--trust", "--mode", "hybrid")...); code != 0 {
		t.Fatalf("trust: %s", stderr)
	}
	if _, stderr, code := runCLI(t, append(g, "host", "restart", "app")...); code != 0 {
		t.Fatalf("restart: %s", stderr)
	}
	if cmd := strings.Join(readManifestAt(t, stateDir, project).HostProcs["app"].Cmdline, " "); !strings.HasSuffix(cmd, "sleep 31") {
		t.Fatalf("restart must run the plan's command: %s", cmd)
	}
}

// An untrusted plan never runs: restart keeps the recorded command and says why.
func TestHostRestartKeepsTheRecordedCommandWhenThePlanIsNotTrusted(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "start", "app", "--", "sleep", "30")...); code != 0 {
		t.Fatalf("start: %s", stderr)
	}
	writeKnowledgeJSON(t, root, restartPlanJSON)
	_, stderr, code := runCLI(t, append(g, "host", "restart", "app")...)
	if code != 0 {
		t.Fatalf("restart: %s", stderr)
	}
	if !strings.Contains(stderr, "not trusted here") {
		t.Errorf("no reason given:\n%s", stderr)
	}
	if cmd := strings.Join(readManifestAt(t, stateDir, project).HostProcs["app"].Cmdline, " "); !strings.HasSuffix(cmd, "sleep 30") {
		t.Fatalf("untrusted plan must not run: %s", cmd)
	}
}
