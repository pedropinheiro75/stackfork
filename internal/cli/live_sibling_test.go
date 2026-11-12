package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

// TestLiveStartEnvFromSiblingRecipe brings up a new worktree whose recipe
// comes only from a prepared sibling worktree (tracked compose file +
// untracked .agent-dev adapter and runtime.env), then takes it down.
func TestLiveStartEnvFromSiblingRecipe(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-sibling")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	mustRun(t, "docker", "pull", "-q", "alpine:latest")

	before := countDocker(t)
	scratch := t.TempDir()
	global := []string{"--state-dir", filepath.Join(scratch, "state"), "--legacy-state-dir", filepath.Join(scratch, "legacy")}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}
	repo := filepath.Join(scratch, "sib")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	mustRun(t, bin, "--root", repo, "--state-dir", filepath.Join(scratch, "learn-state"), "learn")
	for f, c := range map[string]string{
		"Dockerfile":         "FROM alpine:latest\nCMD [\"sh\", \"-c\", \"echo $CHECKOUT_MARK; sleep 3600\"]\n",
		"docker-compose.yml": "services:\n  app:\n    build: .\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n",
		"f":                  "x",
	} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "git", "-C", repo, "add", "-A")
	mustRun(t, "git", "-C", repo, "-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "init")
	sib := filepath.Join(repo, ".worktrees", "a")
	nw := filepath.Join(repo, ".worktrees", "b")
	mustRun(t, "git", "-C", repo, "worktree", "add", "-q", "-b", "a", sib)
	mustRun(t, "git", "-C", repo, "worktree", "add", "-q", "-b", "b", nw)
	ad := filepath.Join(sib, ".agent-dev")
	os.MkdirAll(ad, 0o755)
	os.WriteFile(filepath.Join(ad, "compose.yml"), []byte("services:\n  app:\n    env_file: ./.agent-dev/runtime.env\n"), 0o644)
	os.WriteFile(filepath.Join(ad, "runtime.env"), []byte("CHECKOUT_MARK="+sib+"\n"), 0o600)

	var projects []string
	defer func() {
		for _, p := range projects {
			exec.Command("docker", "rm", "-f", p+"-app-1").Run()
			exec.Command("docker", "volume", "rm", p+"_data").Run()
			exec.Command("docker", "network", "rm", p+"_default").Run()
			exec.Command("docker", "image", "rm", p+"-app:dev").Run()
		}
		before.assertBaseline(t, func() dockerCounts { return countDocker(t) }, projects)
	}()
	project := func(root string) string {
		out, code := sf("--root", root, "where")
		if code != 0 {
			t.Fatalf("where: %s", out)
		}
		i := strings.Index(out, `"project": "`)
		j := strings.Index(out[i+12:], `"`)
		p := out[i+12 : i+12+j]
		projects = append(projects, p)
		return p
	}
	pa, pb := project(sib), project(nw)
	if out, code := sf("--root", sib, "prepare", "-f", "docker-compose.yml", "-f", ".agent-dev/compose.yml", "--env-file", ".agent-dev/runtime.env"); code != 0 {
		t.Fatalf("prepare sibling: %s", out)
	}
	// learn was verified in the sibling worktree
	if err := verifiedFixture(&knowledge.Knowledge{Status: knowledge.StatusVerified}).Save(filepath.Join(sib, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	out, code := sf("start-env", nw)
	if code != 0 || !strings.Contains(out, "reusing the recipe of "+pa) || !strings.Contains(out, "started "+pb) {
		t.Fatalf("start-env code=%d\n%s", code, out)
	}
	if !dockerHas(t, "container", pb+"-app-1") {
		t.Fatal("new worktree's container is not up")
	}
	if logs, _ := sf("--root", nw, "logs", "app"); !strings.Contains(logs, nw) || strings.Contains(logs, sib) {
		t.Errorf("container did not get the rewritten env:\n%s", logs)
	}
	if out, code := sf("--root", nw, "down", "--volumes"); code != 0 {
		t.Fatalf("down: %s", out)
	}
}
