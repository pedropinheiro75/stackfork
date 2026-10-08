package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveStartEnv starts a scratch "ready" repo through `start-env`, then a
// second worktree of it created with `worktree add`, proving the two get
// separate instances and that nothing outside the scratch projects changes.
func TestLiveStartEnv(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-startenv")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())
	mustRun(t, "docker", "pull", "-q", "alpine:latest")

	before := countDocker(t)
	scratch := t.TempDir()
	global := []string{"--state-dir", filepath.Join(scratch, "state"), "--legacy-state-dir", filepath.Join(scratch, "legacy")}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}

	repo := filepath.Join(scratch, "ready")
	if err := os.MkdirAll(filepath.Join(repo, ".agent-dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	files := map[string]string{
		"Dockerfile":                  "FROM alpine:latest\nCMD [\"sleep\", \"3600\"]\n",
		".agent-dev/app.compose.yaml": "services:\n  app:\n    build: ..\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n",
		"f":                           "x",
	}
	for f, c := range files {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "git", "-C", repo, "add", "-A")
	mustRun(t, "git", "-C", repo, "-c", "user.email=t@e.x", "-c", "user.name=t", "commit", "-q", "-m", "init")
	mustRun(t, "git", "-C", repo, "branch", "other")

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
	p1 := project(repo)

	out, code := sf("start-env", repo)
	if code != 0 || !strings.Contains(out, "started "+p1) {
		t.Fatalf("start-env code=%d\n%s", code, out)
	}
	if !dockerHas(t, "container", p1+"-app-1") {
		t.Fatal("main checkout's container is not up")
	}

	out, code = sf("worktree", "add", repo, "other")
	if code != 0 || !strings.Contains(out, "created") {
		t.Fatalf("worktree add code=%d\n%s", code, out)
	}
	wt := filepath.Join(repo, ".worktrees", "other")
	p2 := project(wt)
	if p1 == p2 {
		t.Fatal("worktree must get its own instance")
	}
	out, code = sf("start-env", wt)
	if code != 0 || !strings.Contains(out, "started "+p2) {
		t.Fatalf("start-env worktree code=%d\n%s", code, out)
	}
	if !dockerHas(t, "container", p1+"-app-1") || !dockerHas(t, "container", p2+"-app-1") {
		t.Fatal("both instances should be up side by side")
	}
	for _, r := range []string{repo, wt} {
		if out, code := sf("--root", r, "down", "--volumes"); code != 0 {
			t.Fatalf("down: %s", out)
		}
	}
}
