package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// TestLiveResumeRestoresContainersAndKeepsData: with a real Docker, up, lose
// the containers (what a reboot does), `resume` brings them back on the same
// data volume and the seed does not run again. Only this test's own project
// is touched.
func TestLiveResumeRestoresContainersAndKeepsData(t *testing.T) {
	if os.Getenv("STACKFORK_LIVE") != "1" {
		t.Skip("set STACKFORK_LIVE=1")
	}
	requireDocker(t)
	root := initRepo(t)
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte("services:\n  app:\n    image: busybox:1.36\n    command: [\"sleep\", \"3600\"]\n    volumes:\n      - data:/d\nvolumes:\n  data: {}\n"), 0o600)
	stateDir := filepath.Join(t.TempDir(), "state")
	m := preparedManifest(t, root, stateDir, "", "-f", composePath)
	project := m.Project
	t.Cleanup(func() {
		runCLI(t, "--root", root, "--state-dir", stateDir, "down", "--volumes")
	})
	// a recorded seed: it appends a line to the volume each time it runs
	mp := filepath.Join(stateDir, project, "manifest.json")
	m.SeedOnce, m.SeedService = `sh -c "echo s >> /d/log"`, "app"
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLI(t, "--root", root, "--state-dir", stateDir, "up"); code != 0 {
		t.Fatalf("up: %s", stderr)
	}
	readLog := func() string {
		out, _ := exec.Command("docker", "run", "--rm", "-v", project+"_data:/d", "busybox:1.36", "cat", "/d/log").CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	if got := readLog(); got != "s" {
		t.Fatalf("the first up must seed once, log=%q", got)
	}
	// the reboot: containers gone, volume stays
	if out, err := exec.Command("sh", "-c", "docker ps -aq --filter label=com.docker.compose.project="+project+" | xargs docker rm -f").CombinedOutput(); err != nil {
		t.Fatalf("rm: %v %s", err, out)
	}
	out, stderr, code := runCLI(t, "--state-dir", stateDir, "resume")
	if code != 0 || !strings.Contains(out, "starting "+project) {
		t.Fatalf("resume: code=%d out=%s err=%s", code, out, stderr)
	}
	ps, _ := exec.Command("docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+project).Output()
	if strings.TrimSpace(string(ps)) == "" {
		t.Fatal("the container is not back")
	}
	if got := readLog(); got != "s" {
		t.Fatalf("resume must not seed again, log=%q", got)
	}
}
