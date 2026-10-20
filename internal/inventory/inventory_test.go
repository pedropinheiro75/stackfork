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
// proxy's IsRunning probe) reports $DOCKER_FAKE_PROXY_RUNNING (default
// "false"), everything else succeeds silently.
func installFakeDocker(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "ps" ]; then
  printf '%s' "$DOCKER_FAKE_PS"
  exit 0
fi
if [ "$1" = "inspect" ]; then
  case " $* " in
    *"State.Health.Log"*) shift; shift; shift; for id in "$@"; do printf '%s %s\n' "$id" "$DOCKER_FAKE_HEALTH_LOG"; done; exit 0 ;;
  esac
  printf '%s' "${DOCKER_FAKE_PROXY_RUNNING:-false}"
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

func TestOptionalProfileServicesAreNotStopped(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-opt-1", root, "main", []string{"web", "db", "nginx", "playwright", "worker"}, nil)
	cfg := map[string]interface{}{"services": map[string]interface{}{
		"web": map[string]interface{}{}, "db": map[string]interface{}{}, "worker": map[string]interface{}{},
		"nginx":      map[string]interface{}{"profiles": []interface{}{"proxy"}},
		"playwright": map[string]interface{}{"profiles": []interface{}{"e2e"}},
	}}
	if err := state.WritePrivate(filepath.Join(base, "sf-opt-1", "compose.json"), cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_FAKE_PS", strings.Join([]string{
		psLine(t, "sf-opt-1", "web", "running", "Up 1 minute", ""),
		psLine(t, "sf-opt-1", "db", "running", "Up 1 minute", ""),
	}, "\n")+"\n")
	inv, err := Collect(Options{NewStateBase: base, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	inst := inv.Instances[0]
	got := map[string]string{}
	for _, s := range inst.Services {
		got[s.Name] = s.State
	}
	for _, n := range []string{"nginx", "playwright", "worker"} {
		if got[n] != ServiceOptional {
			t.Errorf("%s: want optional, got %q", n, got[n])
		}
	}
	if inst.State != StateRunning {
		t.Errorf("instance %q, want running", inst.State)
	}
	if len(inst.Warnings) != 0 {
		t.Errorf("unexpected warnings %v", inst.Warnings)
	}

	// an exited container is still a real stop, and makes the instance partial
	t.Setenv("DOCKER_FAKE_PS", strings.Join([]string{
		psLine(t, "sf-opt-1", "web", "running", "Up 1 minute", ""),
		psLine(t, "sf-opt-1", "db", "exited", "Exited (1) 1 minute ago", ""),
	}, "\n")+"\n")
	inv, _ = Collect(Options{NewStateBase: base, LegacyStateBase: t.TempDir(), Version: "test"})
	if inv.Instances[0].State != StatePartial || len(inv.Instances[0].Warnings) == 0 {
		t.Errorf("exited db: state %q warnings %v", inv.Instances[0].State, inv.Instances[0].Warnings)
	}

	// nothing running: profile services stay optional, the rest are stopped
	t.Setenv("DOCKER_FAKE_PS", "")
	inv, _ = Collect(Options{NewStateBase: base, LegacyStateBase: t.TempDir(), Version: "test"})
	for _, s := range inv.Instances[0].Services {
		want := StateStopped
		if s.Name == "nginx" || s.Name == "playwright" {
			want = ServiceOptional
		}
		if s.State != want {
			t.Errorf("down: %s is %q, want %q", s.Name, s.State, want)
		}
	}
	if inv.Instances[0].State != StateStopped {
		t.Errorf("down: instance %q", inv.Instances[0].State)
	}
}

func TestSeedWarningOnlyWhenASeedFailed(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"db:seed":"bun scripts/seed"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeInstance(t, base, "sf-seed-1", root, "main", []string{"web"}, nil)
	mp := filepath.Join(base, "sf-seed-1", "manifest.json")
	set := func(once string, seeded *bool) Instance {
		m, _ := state.ReadManifest(mp)
		m.SeedOnce, m.Seeded = once, seeded
		if err := state.WritePrivate(mp, m); err != nil {
			t.Fatal(err)
		}
		inv, _ := Collect(Options{NewStateBase: base, LegacyStateBase: t.TempDir(), Version: "test"})
		return inv.Instances[0]
	}
	f, tr := false, true
	run := "stackfork --instance sf-seed-1 seed"
	if i := set("", nil); i.SeedCommand != "" || i.SeedForce != "" {
		t.Errorf("an existing instance (no seed fields) must show nothing: %+v", i)
	}
	if i := set("", &tr); i.SeedCommand != "" {
		t.Errorf("seeded: %+v", i)
	}
	if i := set("", &f); i.SeedCommand != run || i.SeedForce != run+" --force" {
		t.Errorf("failed (auto): %+v", i)
	}
	os.WriteFile(filepath.Join(base, "sf-seed-1", state.SeedLog), []byte("$ seed\nerror: duplicate key\n    at x (y.js:1)\n"), 0o600)
	if i := set("", &f); i.SeedDone != run+" --mark-done" || i.SeedError != "$ seed\nerror: duplicate key" {
		t.Errorf("failed with log: %+v", i)
	}
	if i := set("bun run db:seed", &f); i.SeedCommand != run {
		t.Errorf("failed (recorded): %+v", i)
	}
	if i := set("none", &f); i.SeedCommand != "" {
		t.Errorf("never seed must not warn: %+v", i)
	}
}

// psRow is a `docker ps --format json` line with an id, a name and extra labels.
func psRow(t *testing.T, project, service, name, state, status string, oneoff bool) string {
	t.Helper()
	labels := "com.docker.compose.project=" + project + ",com.docker.compose.service=" + service + ",io.stackfork.root=/x"
	if oneoff {
		labels += ",com.docker.compose.oneoff=True"
	} else {
		labels += ",com.docker.compose.oneoff=False"
	}
	b, _ := json.Marshal(map[string]string{"ID": "id-" + name, "Names": name, "Labels": labels, "State": state, "Status": status})
	return string(b)
}

func collectOne(t *testing.T, base string) Instance {
	t.Helper()
	inv, err := Collect(Options{NewStateBase: base, LegacyStateBase: t.TempDir(), Version: "test"})
	if err != nil || len(inv.Instances) != 1 {
		t.Fatalf("%v %+v", err, inv.Instances)
	}
	return inv.Instances[0]
}

func svcStates(i Instance) map[string]string {
	m := map[string]string{}
	for _, s := range i.Services {
		m[s.Name] = s.State + "/" + s.Health
	}
	return m
}

func TestUnhealthyServicesAreGroupedAndTimeoutIsNamed(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-311", root, "main", []string{"app", "db", "nginx"}, nil)
	t.Setenv("DOCKER_FAKE_HEALTH_LOG", `[{"Output":"old"},{"Output":"pg_isready: Health check exceeded timeout (3s)\n"}]`)
	t.Setenv("DOCKER_FAKE_PS", strings.Join([]string{
		psRow(t, "sf-311", "app", "sf-311-app-1", "running", "Up 17 hours (unhealthy)", false),
		psRow(t, "sf-311", "db", "sf-311-db-1", "running", "Up 18 hours (unhealthy)", false),
		psRow(t, "sf-311", "nginx", "sf-311-nginx-1", "running", "Up 18 hours", false),
	}, "\n")+"\n")
	i := collectOne(t, base)
	want := "app, db unhealthy: health check timing out (Docker may be overloaded or the service is slow to answer) - try `stackfork restart app db`"
	if len(i.Warnings) != 1 || i.Warnings[0] != want {
		t.Fatalf("warnings %q", i.Warnings)
	}
	if st := svcStates(i); st["app"] != "running/unhealthy" || st["db"] != "running/unhealthy" || st["nginx"] != "running/" {
		t.Fatalf("states %v", st)
	}
}

func TestOtherHealthFailureShowsRedactedLastLine(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	writeInstance(t, base, "sf-1", gitRepo(t, "main"), "main", []string{"db"}, nil)
	t.Setenv("DOCKER_FAKE_HEALTH_LOG", `[{"Output":"warming up\nFATAL: password=hunter2 rejected "}]`)
	t.Setenv("DOCKER_FAKE_PS", psRow(t, "sf-1", "db", "sf-1-db-1", "running", "Up 1 minute (unhealthy)", false)+"\n")
	w := collectOne(t, base).Warnings
	if len(w) != 1 || !strings.Contains(w[0], "FATAL: password=*** rejected") || strings.Contains(w[0], "hunter2") {
		t.Fatalf("warnings %q", w)
	}
}

func TestCreatedIsNotStartedNotOptionalAndOneOffsAreSeparate(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-312", root, "main", []string{"app", "db", "nginx"}, nil)
	t.Setenv("DOCKER_FAKE_HEALTH_LOG", `[{"Output":"Health check exceeded timeout (3s)"}]`)
	t.Setenv("DOCKER_FAKE_PS", strings.Join([]string{
		psRow(t, "sf-312", "db", "sf-312-db-1", "running", "Up 1 hour (unhealthy)", false),
		psRow(t, "sf-312", "app", "sf-312-app-1", "created", "Created", false),
		psRow(t, "sf-312", "nginx", "sf-312-nginx-1", "created", "Created", false),
		psRow(t, "sf-312", "app", "sf-312-app-run-44991bcf731a", "running", "Up 12 minutes (unhealthy)", true),
		psRow(t, "sf-312", "app", "sf-312-app-run-fc1dbee3e9d1", "created", "Created", true),
	}, "\n")+"\n")
	i := collectOne(t, base)
	if st := svcStates(i); st["app"] != "not-started/" || st["nginx"] != "not-started/" || st["db"] != "running/unhealthy" {
		t.Fatalf("states %v", st)
	}
	if i.State != StatePartial {
		t.Errorf("instance %q, want partial (not-started counts)", i.State)
	}
	if len(i.Warnings) != 2 || !strings.Contains(i.Warnings[0], "db unhealthy") || i.Warnings[1] != "app, nginx not started - run `stackfork up`" {
		t.Fatalf("warnings %q", i.Warnings)
	}
	if i.OneOffRunning != 1 || i.OneOffLeftover != 1 || i.OneOffCleanup != "docker rm sf-312-app-run-fc1dbee3e9d1" {
		t.Fatalf("one-offs %+v", i)
	}
}

func TestCreatedServiceWaitingForAStartingDependencyIsNotAnAlarm(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-w", root, "main", []string{"app", "nginx", "worker"}, nil)
	cfg := map[string]interface{}{"services": map[string]interface{}{
		"app":    map[string]interface{}{},
		"nginx":  map[string]interface{}{"depends_on": map[string]interface{}{"app": map[string]interface{}{"condition": "service_healthy"}}},
		"worker": map[string]interface{}{},
	}}
	if err := state.WritePrivate(filepath.Join(base, "sf-w", "compose.json"), cfg); err != nil {
		t.Fatal(err)
	}
	ps := func(appStatus string) {
		t.Setenv("DOCKER_FAKE_PS", strings.Join([]string{
			psRow(t, "sf-w", "app", "sf-w-app-1", "running", appStatus, false),
			psRow(t, "sf-w", "nginx", "sf-w-nginx-1", "created", "Created", false),
			psRow(t, "sf-w", "worker", "sf-w-worker-1", "created", "Created", false),
		}, "\n")+"\n")
	}
	ps("Up 1 minute (health: starting)")
	i := collectOne(t, base)
	st := svcStates(i)
	if st["nginx"] != "waiting/" || st["worker"] != "not-started/" {
		t.Fatalf("states %v", st)
	}
	if i.State != StatePartial {
		t.Errorf("instance %q", i.State)
	}
	if len(i.Warnings) != 2 || i.Warnings[0] != "nginx waiting for app to become healthy" || i.Warnings[1] != "worker not started - run `stackfork up`" {
		t.Fatalf("warnings %q", i.Warnings)
	}
	if w := i.Services[1].WaitingFor; len(w) != 1 || w[0] != "app" {
		t.Fatalf("waiting_for %v", w)
	}
	// once the dependency is up, a service still never started IS a problem
	ps("Up 2 minutes (healthy)")
	if st := svcStates(collectOne(t, base)); st["nginx"] != "not-started/" {
		t.Fatalf("states %v", st)
	}
}

func TestRelRoot(t *testing.T) {
	main := t.TempDir()
	if err := os.Mkdir(filepath.Join(main, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	loose := t.TempDir()
	home, _ := os.UserHomeDir()
	for root, want := range map[string]string{
		main:                                  "main checkout",
		main + "/.worktrees/feature-login":    ".worktrees/feature-login",
		loose:                                 loose,
		filepath.Join(home, "nowhere", "app"): "~/nowhere/app",
	} {
		if got := relRoot(root); got != want {
			t.Errorf("relRoot(%q) = %q, want %q", root, got, want)
		}
	}
}

func TestAppName(t *testing.T) {
	parent := t.TempDir()
	main := filepath.Join(parent, "acme-web")
	wt := filepath.Join(parent, "elsewhere", "feat")
	for _, d := range []string{filepath.Join(main, ".git", "worktrees", "feat"), wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitfile := "gitdir: " + filepath.Join(main, ".git", "worktrees", "feat") + "\n"
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(gitfile), 0o644); err != nil {
		t.Fatal(err)
	}
	for root, want := range map[string]string{
		main:                             "acme-web",
		main + "/.worktrees/328-add-x":   "acme-web",
		wt:                               "acme-web",
		t.TempDir():                      "",
		filepath.Join(parent, "missing"): "",
	} {
		if got := appName(root); got != want {
			t.Errorf("appName(%q) = %q, want %q", root, got, want)
		}
	}
}

func TestInventoryListsHostProcessAsService(t *testing.T) {
	installFakeDocker(t)
	base := t.TempDir()
	root := gitRepo(t, "main")
	writeInstance(t, base, "sf-hostrow-1", root, "main", []string{"db"}, nil)
	mp := filepath.Join(base, "sf-hostrow-1", "manifest.json")
	m, err := state.ReadManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	start, err := state.ProcStart(os.Getpid())
	if err != nil {
		t.Skip("no process start time on this platform")
	}
	port := startListener(t)
	m.HostProcs = map[string]state.HostProc{
		"app":  {PID: os.Getpid(), StartedAt: start, Cmdline: os.Args, Port: port},
		"gone": {PID: 999999, StartedAt: "1970-01-01T00:00:00Z", Cmdline: []string{"x"}, Port: 40999},
	}
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	inst := collectOne(t, base)
	got := map[string]Service{}
	for _, s := range inst.Services {
		got[s.Name] = s
	}
	app, gone := got["host:app"], got["host:gone"]
	if app.Label != "app" || app.Kind != "host" || app.State != "running" || app.Image != "host" {
		t.Fatalf("app: %+v", app)
	}
	want := "http://127.0.0.1:" + strconv.Itoa(port)
	if len(app.Endpoints) != 1 || app.Endpoints[0].URL != want {
		t.Errorf("app endpoints %+v, want %s", app.Endpoints, want)
	}
	if gone.Kind != "host" || gone.State != "exited" {
		t.Errorf("gone: %+v", gone)
	}
	if inst.State != StateStopped {
		t.Errorf("a host row must not change the instance state, got %s", inst.State)
	}
}
