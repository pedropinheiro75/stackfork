package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// TestLiveReset: `reset` deletes a scratch instance's volume, starts it again
// from zero and seeds the new database, and a reset without --yes and without
// a terminal deletes nothing. Temp state dir; alpine:latest is already on the
// machine, nothing is pulled.
func TestLiveReset(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-reset")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())

	before := countDocker(t)
	scratch := t.TempDir()
	stateDir := filepath.Join(scratch, "state")
	global := []string{"--state-dir", stateDir, "--legacy-state-dir", filepath.Join(scratch, "legacy")}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}
	repo := filepath.Join(scratch, "resetrepo")
	os.MkdirAll(repo, 0o755)
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	for f, c := range map[string]string{
		"docker-compose.yml": "services:\n  app:\n    image: alpine:latest\n    command: [\"sleep\", \"3600\"]\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n",
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
	}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	out, _ := sf("--root", repo, "where")
	i := strings.Index(out, `"project": "`)
	j := strings.Index(out[i+12:], `"`)
	p := out[i+12 : i+12+j]
	defer func() {
		sf("--root", repo, "down", "--volumes")
		exec.Command("docker", "rm", "-f", p+"-app-1").Run()
		exec.Command("docker", "volume", "rm", p+"_data").Run()
		exec.Command("docker", "network", "rm", p+"_default").Run()
		before.assertBaseline(t, func() dockerCounts { return countDocker(t) }, []string{p})
	}()

	if out, code := sf("start-env", repo); code != 0 {
		t.Fatalf("start-env: %s", out)
	}
	if out, code := sf("--root", repo, "exec", "app", "sh", "-c", "touch /data/marker"); code != 0 {
		t.Fatalf("marker: %s", out)
	}

	// no terminal, no --yes: refused, the data stays
	if out, code := sf("--root", repo, "reset"); code == 0 || !strings.Contains(out, "--yes") {
		t.Fatalf("reset without --yes must be refused (code %d):\n%s", code, out)
	}
	if out, code := sf("--root", repo, "exec", "app", "ls", "/data/marker"); code != 0 {
		t.Fatalf("a refused reset lost the data: %s", out)
	}

	out, code := sf("--root", repo, "reset", "--yes")
	if code != 0 || !strings.Contains(out, "reset and started "+p) {
		t.Fatalf("reset code=%d\n%s", code, out)
	}
	if out, code := sf("--root", repo, "exec", "app", "ls", "/data/marker"); code == 0 {
		t.Fatalf("the old volume survived the reset: %s", out)
	}
	if out, _ := sf("--root", repo, "exec", "app", "cat", "/data/seed.log"); strings.Count(out, "seeded") != 1 {
		t.Fatalf("the new database must be seeded once, got: %s", out)
	}
	r, err := runs.Get(filepath.Join(stateDir, p), 2)
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, s := range r.Steps {
		states = append(states, s.ID+"="+s.State)
	}
	if r.Command != "reset" || r.Outcome != runs.Succeeded || strings.Join(states, " ") != "sync=succeeded secrets=skipped down=succeeded prepare=succeeded up=succeeded seed=succeeded" {
		t.Fatalf("%s %s %v", r.Command, r.Outcome, states)
	}
}
