package dashboard_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/cli"
	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/runs"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func twoServiceFixture(t *testing.T) (root, newBase, legacyBase, project, log string) {
	t.Helper()
	installFakeDocker(t)
	log = filepath.Join(t.TempDir(), "docker.log")
	t.Setenv("DOCKER_FAKE_LOG", log)
	root = t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "-b", "main", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	composePath := filepath.Join(root, "compose.yaml")
	os.WriteFile(composePath, []byte("services:\n  app:\n    image: busybox\n  db:\n    image: postgres:17\n"), 0o600)
	newBase, legacyBase = t.TempDir(), t.TempDir()
	var out, errBuf bytes.Buffer
	if code := cli.Run([]string{"--root", root, "--state-dir", newBase, "prepare", "--force", "-f", composePath}, &out, &errBuf); code != 0 {
		t.Fatalf("prepare: %s", errBuf.String())
	}
	var p struct {
		Project string `json:"project"`
	}
	json.Unmarshal(out.Bytes(), &p)
	return root, newBase, legacyBase, p.Project, log
}

// A hybrid plan replaces `app` with a host app: resume must not start its container.
func TestResumeUpSkipsServiceTheHybridPlanReplaces(t *testing.T) {
	root, newBase, legacyBase, project, log := twoServiceFixture(t)
	hybridPlan(t, root, "web")
	setMode(t, root, "hybrid")
	srv, _ := dashboard.New(newBase, legacyBase, "test", 55303)
	srv.ResumeUp(project)
	b, _ := os.ReadFile(log)
	var up string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, " up -d ") {
			up = l
		}
	}
	if up == "" || !strings.HasSuffix(up, " db") {
		t.Fatalf("resume must up only db, got %q", up)
	}
}

// Resume never seeds: a pending seed stays pending and the card says so.
func TestResumeUpDoesNotRunAPendingSeed(t *testing.T) {
	root, newBase, legacyBase, project, log := twoServiceFixture(t)
	mp := filepath.Join(newBase, project, "manifest.json")
	m, _ := state.ReadManifest(mp)
	m.SeedOnce, m.SeedService, m.SeedPending = "echo seed", "db", true
	state.WritePrivate(mp, m)
	_ = root
	srv, _ := dashboard.New(newBase, legacyBase, "test", 55304)
	if ok, msg := srv.ResumeUp(project); !ok {
		t.Fatal(msg)
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), " exec ") {
		t.Fatalf("the seed ran:\n%s", b)
	}
	if m, _ = state.ReadManifest(mp); !m.SeedPending {
		t.Fatal("the seed must stay pending")
	}
	r := runs.Latest(filepath.Join(newBase, project))
	found := false
	for _, st := range r.Steps {
		found = found || (st.ID == "seed" && st.State == runs.Skipped && strings.Contains(st.Reason, "seed pending"))
	}
	if !found {
		t.Fatalf("no skipped seed step on the run: %+v", r.Steps)
	}
}

// A checkout on another branch is not resumed; a held instance lock blocks it too.
func TestResumeBlock(t *testing.T) {
	root, newBase, _, project, _ := twoServiceFixture(t)
	exec.Command("git", "-C", root, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "x").Run()
	in := inventory.Instance{Project: project, Root: root, Branch: "main"}
	dir := filepath.Join(newBase, project)
	if why := dashboard.ResumeBlock(in, dir); why != "" {
		t.Fatalf("on its branch: %q", why)
	}
	exec.Command("git", "-C", root, "checkout", "-q", "-b", "other").Run()
	if why := dashboard.ResumeBlock(in, dir); !strings.Contains(why, "not on main") {
		t.Fatalf("other branch: %q", why)
	}
	exec.Command("git", "-C", root, "checkout", "-q", "main").Run()
	l, err := state.TryLock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if why := dashboard.ResumeBlock(in, dir); !strings.Contains(why, "in progress") {
		t.Fatalf("held lock: %q", why)
	}
	_ = time.Second
}
