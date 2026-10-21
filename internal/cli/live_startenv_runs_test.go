package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// TestLiveStartEnvRuns: a Start with a data seed, a slow exec step and a host
// helper records a succeeded run with real Docker; a second Start while the
// first one runs is refused and writes no run.
func TestLiveStartEnvRuns(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-runs")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	// alpine:latest is already on the machine: nothing is pulled

	before := countDocker(t)
	scratch := t.TempDir()
	stateDir := filepath.Join(scratch, "state")
	global := []string{"--state-dir", stateDir, "--legacy-state-dir", filepath.Join(scratch, "legacy")}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}
	repo := filepath.Join(scratch, "runsrepo")
	os.MkdirAll(repo, 0o755)
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	for f, c := range map[string]string{
		"docker-compose.yml": "services:\n  app:\n    image: alpine:latest\n    command: [\"sleep\", \"3600\"]\n    labels:\n      io.stackfork.http: \"3000\"\n    ports:\n      - \"3000\"\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n",
		"f":                  "x",
	} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "git", "-C", repo, "add", "-A")
	mustRun(t, "git", "-C", repo, "-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "init")
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified, Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
		{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", "docker-compose.yml"}},
		{ID: "up", Kind: knowledge.KindComposeUp},
		{ID: "seed", Kind: knowledge.KindExec, Once: knowledge.OnceData, Service: "app", Cmd: "echo seeded >> /data/seed.log"},
		{ID: "wait", Kind: knowledge.KindExec, Service: "app", Cmd: "sleep 6"},
		{ID: "helper", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "helper", Cmd: "sleep 3600"},
	}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	out, _ := sf("--root", repo, "where")
	i := strings.Index(out, `"project": "`)
	j := strings.Index(out[i+12:], `"`)
	p := out[i+12 : i+12+j]
	defer func() {
		sf("--root", repo, "host", "stop", "helper")
		sf("--root", repo, "down", "--volumes")
		exec.Command("docker", "rm", "-f", p+"-app-1").Run()
		exec.Command("docker", "volume", "rm", p+"_data").Run()
		exec.Command("docker", "network", "rm", p+"_default").Run()
		before.assertBaseline(t, func() dockerCounts { return countDocker(t) }, []string{p})
	}()
	if out, code := sf("--root", repo, "plan", "--trust"); code != 0 {
		t.Fatalf("plan --trust: %s", out)
	}
	if out, code := sf("config", "set-mode", repo, "hybrid"); code != 0 {
		t.Fatalf("set-mode: %s", out)
	}

	instanceDir := filepath.Join(stateDir, p)
	var first bytes.Buffer
	cmd := exec.Command(bin, append(append([]string{}, global...), "start-env", repo)...)
	cmd.Stdout, cmd.Stderr = &first, &first
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer cmd.Process.Kill()

	// wait for the slow step to be running
	deadline := time.Now().Add(120 * time.Second)
	for {
		if r, err := runs.Get(instanceDir, 1); err == nil && stepState(r, "wait") == runs.StepRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slow step never ran:\n%s", first.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	out, code := sf("start-env", repo)
	if code == 0 || !strings.Contains(out, "another run is in progress") || !strings.Contains(out, "run 1, start-env from cli") {
		t.Fatalf("second Start must be refused (code %d):\n%s", code, out)
	}
	if err := <-done; err != nil {
		t.Fatalf("first Start: %v\n%s", err, first.String())
	}
	r, err := runs.Get(instanceDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, s := range r.Steps {
		states = append(states, s.ID+"="+s.State)
	}
	if r.Outcome != runs.Succeeded || strings.Join(states, " ") != "sync=succeeded secrets=skipped prepare=succeeded up=succeeded seed=succeeded wait=succeeded helper=succeeded" {
		t.Fatalf("%s %v\n%s", r.Outcome, states, first.String())
	}
	if files, _ := filepath.Glob(filepath.Join(instanceDir, "runs", "*.json")); len(files) != 1 {
		t.Fatalf("the refused Start wrote a run: %v", files)
	}
	if b, err := runs.Log(instanceDir, 1, "seed"); err != nil || !strings.Contains(string(b), "seeded new database") {
		t.Fatalf("seed log %q %v", b, err)
	}
	if b, _ := runs.Log(instanceDir, 1, "up"); !strings.Contains(string(b), "starting "+p) {
		t.Fatalf("up log %q", b)
	}
	if out, _ := sf("--root", repo, "exec", "app", "cat", "/data/seed.log"); strings.Count(out, "seeded") != 1 {
		t.Fatalf("seeded %d times: %s", strings.Count(out, "seeded"), out)
	}
}

func stepState(r *runs.Run, id string) string {
	for _, s := range r.Steps {
		if s.ID == id {
			return s.State
		}
	}
	return ""
}
