package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveResetFromMain: a worktree prepared with one Compose file resets from
// a main checkout prepared with two, then starts with the override applied;
// its old untracked override is under .stackfork-aside. Temp state dir;
// alpine:latest is already on the machine, nothing is pulled.
func TestLiveResetFromMain(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-frommain")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())

	before := countDocker(t)
	scratch := t.TempDir()
	global := []string{"--state-dir", filepath.Join(scratch, "state"), "--legacy-state-dir", filepath.Join(scratch, "legacy")}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}
	repo := filepath.Join(scratch, "frommain")
	os.MkdirAll(filepath.Join(repo, ".stackfork"), 0o755)
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	mustRun(t, bin, "--root", repo, "--state-dir", filepath.Join(scratch, "learn-state"), "learn")
	for f, c := range map[string]string{
		"docker-compose.yml":          "services:\n  app:\n    image: alpine:latest\n    command: [\"sleep\", \"3600\"]\n",
		"f":                           "x",
		".stackfork/dev.compose.yaml": "services:\n  app:\n    environment:\n      MARK: main\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "git", "-C", repo, "add", "docker-compose.yml", "f")
	mustRun(t, "git", "-C", repo, "-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "init")
	wt := filepath.Join(repo, ".worktrees", "feat")
	mustRun(t, "git", "-C", repo, "worktree", "add", "-q", "-b", "feat", wt)

	project := func(root string) string {
		out, _ := sf("--root", root, "where")
		i := strings.Index(out, `"project": "`)
		j := strings.Index(out[i+12:], `"`)
		return out[i+12 : i+12+j]
	}
	pm, pw := project(repo), project(wt)
	defer func() {
		for _, p := range []string{pm, pw} {
			exec.Command("docker", "rm", "-f", p+"-app-1").Run()
			exec.Command("docker", "network", "rm", p+"_default").Run()
		}
		before.assertBaseline(t, func() dockerCounts { return countDocker(t) }, []string{pm, pw})
	}()

	if out, code := sf("--root", repo, "prepare", "-f", "docker-compose.yml", "-f", ".stackfork/dev.compose.yaml"); code != 0 {
		t.Fatalf("prepare main: %s", out)
	}
	os.MkdirAll(filepath.Join(wt, ".stackfork"), 0o755)
	os.WriteFile(filepath.Join(wt, ".stackfork", "dev.compose.yaml"), []byte("services:\n  app:\n    environment:\n      MARK: old\n"), 0o644)
	if out, code := sf("--root", wt, "prepare", "-f", "docker-compose.yml", "-f", ".stackfork/dev.compose.yaml"); code != 0 {
		t.Fatalf("prepare worktree: %s", out)
	}

	out, code := sf("--root", wt, "reset", "--from-main", "--yes")
	if code != 0 || !strings.Contains(out, "reset and started "+pw) {
		t.Fatalf("reset --from-main code=%d\n%s", code, out)
	}
	if got, _ := sf("--root", wt, "exec", "app", "printenv", "MARK"); strings.TrimSpace(got) != "main" {
		t.Fatalf("the main checkout's override must apply, got %q", got)
	}
	if got, _ := filepath.Glob(filepath.Join(wt, ".stackfork-aside", "*", ".stackfork", "dev.compose.yaml")); len(got) != 1 {
		t.Fatalf("the old override must be under .stackfork-aside: %v", got)
	}
}
