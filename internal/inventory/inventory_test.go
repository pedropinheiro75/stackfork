package inventory

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// startListener binds a free loopback port and holds it for the test's
// duration, so a "busy recorded port" scenario has a real, externally-held
// port to detect.
func startListener(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// installFakeDocker puts a fake `docker` first on PATH: `docker ps --all
// ...` prints $DOCKER_FAKE_PS (JSON lines), `docker inspect ...` (the
// proxy's IsRunning probe) always reports "false", everything else
// succeeds silently.
func installFakeDocker(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "ps" ]; then
  printf '%s' "$DOCKER_FAKE_PS"
  exit 0
fi
if [ "$1" = "inspect" ]; then
  printf 'false'
  exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// gitRepo creates a repo with one commit on the given branch, so
// branchExists's `git show-ref` finds it.
func gitRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", branch)
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false") // never wait on this machine's commit-signing agent for a throwaway fixture repo
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-q", "-m", "init")
	return dir
}

// writeInstance writes manifest.json and compose.json for one instance
// under base/project.
// writeInstance writes a synthetic manifest.json + compose.json.
// declaredPorts optionally lists each service's declared target ports (the
// snapshot's own "ports" entries, task 3.6: an endpoint is built from these
// whether or not the service is currently running), keyed by service name;
// nil/missing means that service declares no ports.
func writeInstance(t *testing.T, base, project, root, branch string, services []string, ports []state.PortAlloc, declaredPorts ...map[string][]int) {
	t.Helper()
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &state.Manifest{
		Project: project, Root: root, Branch: branch, Hostname: project + ".localhost",
		Files: []string{"compose.yaml"}, Inputs: map[string]string{}, Ports: ports,
	}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	var byService map[string][]int
	if len(declaredPorts) > 0 {
		byService = declaredPorts[0]
	}
	svcMap := map[string]interface{}{}
	for _, s := range services {
		svc := map[string]interface{}{}
		if targets := byService[s]; len(targets) > 0 {
			var portsList []interface{}
			for _, target := range targets {
				portsList = append(portsList, map[string]interface{}{
					"target": json.Number(strconv.Itoa(target)), "protocol": "tcp",
				})
			}
			svc["ports"] = portsList
		}
		svcMap[s] = svc
	}
	config := map[string]interface{}{"services": svcMap}
	if err := state.WritePrivate(filepath.Join(dir, "compose.json"), config); err != nil {
		t.Fatal(err)
	}
}

func psLine(t *testing.T, project, service, state, status, ports string) string {
	t.Helper()
	labels := "com.docker.compose.project=" + project + ",com.docker.compose.service=" + service +
		",io.stackfork.root=/x"
	row := map[string]string{"Labels": labels, "State": state, "Status": status, "Ports": ports}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCollectStates(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	legacyBase := t.TempDir()

	running := gitRepo(t, "main")
	partial := gitRepo(t, "main")
	stopped := gitRepo(t, "main")
	missingBranch := gitRepo(t, "main")
	missingCheckout := filepath.Join(t.TempDir(), "gone")

	webDBPorts := map[string][]int{"web": {3000}, "db": {5432}}
	writeInstance(t, newBase, "sf-running-1", running, "main", []string{"web", "db"}, nil, webDBPorts)
	writeInstance(t, newBase, "sf-partial-2", partial, "main", []string{"web", "db"}, nil, webDBPorts)
	writeInstance(t, newBase, "sf-stopped-3", stopped, "main", []string{"web"}, nil)
	writeInstance(t, newBase, "sf-missingbranch-4", missingBranch, "gone-branch", []string{"web"}, nil)
	writeInstance(t, newBase, "sf-missingcheckout-5", missingCheckout, "main", []string{"web"}, nil)
	if err := os.MkdirAll(filepath.Join(newBase, "sf-unknown-6"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newBase, "sf-unknown-6", "manifest.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyRoot := gitRepo(t, "main")
	writeInstance(t, legacyBase, "idv-legacy-7", legacyRoot, "main", []string{"web"}, nil)

	psLines := []string{
		psLine(t, "sf-running-1", "web", "running", "Up 1 minute (healthy)", "127.0.0.1:20001->3000/tcp"),
		psLine(t, "sf-running-1", "db", "running", "Up 1 minute", "127.0.0.1:20002->5432/tcp"),
		psLine(t, "sf-partial-2", "web", "running", "Up 1 minute", "127.0.0.1:20003->3000/tcp"),
		psLine(t, "sf-partial-2", "db", "exited", "Exited (0) 1 minute ago", ""),
		psLine(t, "idv-legacy-7", "web", "running", "Up 1 minute", "127.0.0.1:20004->3000/tcp"),
	}
	t.Setenv("DOCKER_FAKE_PS", strings.Join(psLines, "\n")+"\n")

	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: legacyBase, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Docker.Reachable {
		t.Fatalf("expected docker reachable, got %+v", inv.Docker)
	}
	if len(inv.Instances) != 7 {
		t.Fatalf("expected 7 instances, got %d: %+v", len(inv.Instances), inv.Instances)
	}

	byProject := map[string]Instance{}
	for _, i := range inv.Instances {
		byProject[i.Project] = i
	}

	if got := byProject["sf-running-1"].State; got != StateRunning {
		t.Errorf("running: expected state %q, got %q", StateRunning, got)
	}
	if eps := byProject["sf-running-1"].Services; len(eps) != 2 {
		t.Errorf("running: expected 2 services, got %+v", eps)
	} else {
		for _, s := range eps {
			if s.Name == "web" {
				if s.Health != "healthy" {
					t.Errorf("web: expected health healthy, got %q", s.Health)
				}
				if len(s.Endpoints) != 1 || s.Endpoints[0].Scheme != "http" {
					t.Errorf("web: expected one http endpoint, got %+v", s.Endpoints)
				}
			}
		}
	}

	if got := byProject["sf-partial-2"].State; got != StatePartial {
		t.Errorf("partial: expected state %q, got %q", StatePartial, got)
	}
	if got := byProject["sf-stopped-3"].State; got != StateStopped {
		t.Errorf("stopped: expected state %q, got %q", StateStopped, got)
	}
	if got := byProject["sf-missingbranch-4"].State; got != StateMissingBranch {
		t.Errorf("missing-branch: expected state %q, got %q", StateMissingBranch, got)
	}
	if got := byProject["sf-missingcheckout-5"].State; got != StateMissingCheckout {
		t.Errorf("missing-checkout: expected state %q, got %q", StateMissingCheckout, got)
	}
	if got := byProject["sf-unknown-6"].State; got != StateUnknown {
		t.Errorf("unknown: expected state %q, got %q", StateUnknown, got)
	}
	if len(byProject["sf-unknown-6"].Warnings) == 0 {
		t.Errorf("unknown: expected a warning")
	}
	legacy := byProject["idv-legacy-7"]
	if !legacy.Legacy {
		t.Errorf("legacy: expected Legacy=true")
	}
	if legacy.State != StateRunning {
		t.Errorf("legacy: expected state %q, got %q", StateRunning, legacy.State)
	}
}

func TestCollectKnowledgeStale(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, newBase, "sf-knowledge-1", root, "main", []string{"web"}, nil)

	trackedFile := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(trackedFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sha, err := state.SHA256File(trackedFile)
	if err != nil {
		t.Fatal(err)
	}
	k := &knowledge.Knowledge{
		Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Inputs:       []knowledge.Input{{Path: "compose.yaml", SHA256: sha}},
		Verification: &knowledge.Verification{Date: "2026-09-20"},
	}
	if err := k.Save(filepath.Join(root, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_FAKE_PS", "")

	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == "sf-knowledge-1" {
			inst = i
		}
	}
	if inst.Knowledge == nil || inst.Knowledge.Status != knowledge.StatusVerified {
		t.Fatalf("expected verified knowledge, got %+v", inst.Knowledge)
	}

	// Change the tracked input: verified knowledge should now read as stale.
	if err := os.WriteFile(trackedFile, []byte("services: {changed: true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv, err = Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range inv.Instances {
		if i.Project == "sf-knowledge-1" {
			inst = i
		}
	}
	if inst.Knowledge == nil || inst.Knowledge.Status != knowledge.StatusStale {
		t.Fatalf("expected stale knowledge, got %+v", inst.Knowledge)
	}
	if len(inst.Knowledge.ChangedFiles) != 1 || inst.Knowledge.ChangedFiles[0] != "compose.yaml" {
		t.Fatalf("expected changed file %q (relative to root, as stored - task 13.7/committed-knowledge portability), got %+v", "compose.yaml", inst.Knowledge.ChangedFiles)
	}
	foundWarning := false
	for _, w := range inst.Warnings {
		if strings.Contains(w, "stale") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected a stale-knowledge warning, got %+v", inst.Warnings)
	}
}

func TestCollectBusyPortWarning(t *testing.T) {
	installFakeDocker(t)
	newBase := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, newBase, "sf-busy-1", root, "main", []string{"web"},
		[]state.PortAlloc{{Service: "web", Target: 3000, Host: startListener(t)}})

	// No container for sf-busy-1 in `docker ps`, so its recorded port
	// isn't "owned" by its own containers; bind it externally to simulate
	// something else holding it.
	inv, err := Collect(Options{NewStateBase: newBase, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var inst Instance
	for _, i := range inv.Instances {
		if i.Project == "sf-busy-1" {
			inst = i
		}
	}
	found := false
	for _, w := range inst.Warnings {
		if strings.Contains(w, "reassign-ports") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a busy-port warning, got %+v", inst.Warnings)
	}
}
