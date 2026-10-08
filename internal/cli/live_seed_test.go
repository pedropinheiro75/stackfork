package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveSeedOnceAcrossUpDownUp: a fresh instance is seeded exactly once
// by the up that creates its volume, not by a later down/up or restart.
func TestLiveSeedOnceAcrossUpDownUp(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-seed")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	mustRun(t, "docker", "pull", "-q", "alpine:latest")

	before := countDocker(t)
	scratch := t.TempDir()
	global := []string{"--state-dir", filepath.Join(scratch, "state"), "--legacy-state-dir", filepath.Join(scratch, "legacy")}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}
	repo := filepath.Join(scratch, "seedrepo")
	os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755)
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	for f, c := range map[string]string{
		"docker-compose.yml":        "services:\n  app:\n    image: alpine:latest\n    command: [\"sleep\", \"3600\"]\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n",
		".stackfork/knowledge.json": `{"schema":1,"recipe":{"seed_once":"echo seeded >> /data/seed.log","seed_service":"app"}}`,
		"f":                         "x",
	} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "git", "-C", repo, "add", "-A")
	mustRun(t, "git", "-C", repo, "-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "init")
	out, _ := sf("--root", repo, "where")
	i := strings.Index(out, `"project": "`)
	j := strings.Index(out[i+12:], `"`)
	p := out[i+12 : i+12+j]
	defer func() {
		exec.Command("docker", "rm", "-f", p+"-app-1").Run()
		exec.Command("docker", "volume", "rm", p+"_data").Run()
		exec.Command("docker", "network", "rm", p+"_default").Run()
		before.assertBaseline(t, func() dockerCounts { return countDocker(t) }, []string{p})
	}()
	if out, code := sf("--root", repo, "prepare", "-f", "docker-compose.yml"); code != 0 {
		t.Fatalf("prepare: %s", out)
	}
	seeds := func() int {
		out, _ := sf("--root", repo, "exec", "app", "cat", "/data/seed.log")
		return strings.Count(out, "seeded")
	}
	if out, code := sf("--root", repo, "up"); code != 0 {
		t.Fatalf("up: %s", out)
	}
	if n := seeds(); n != 1 {
		t.Fatalf("after first up: %d seeds", n)
	}
	sf("--root", repo, "down")
	if out, code := sf("--root", repo, "up"); code != 0 {
		t.Fatalf("up 2: %s", out)
	}
	sf("--root", repo, "restart", "app")
	if n := seeds(); n != 1 {
		t.Fatalf("after down/up/restart: %d seeds", n)
	}
	sf("--root", repo, "down", "--volumes")
}
