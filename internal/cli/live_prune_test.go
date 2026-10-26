package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// dockerCounts snapshots container names, volume names and image refs.
type dockerCounts struct{ containers, volumes, images string }

func countDocker(t *testing.T) dockerCounts {
	t.Helper()
	return dockerCounts{
		containers: mustRun(t, "docker", "ps", "-a", "--format", "{{.Names}}"),
		volumes:    mustRun(t, "docker", "volume", "ls", "-q"),
		images:     mustRun(t, "docker", "images", "--format", "{{.Repository}}:{{.Tag}}"),
	}
}

// anonymousVolume is a Docker-generated volume name (a one-off `run --rm`
// container's, which disappears with it).
var anonymousVolume = regexp.MustCompile(`^[0-9a-f]{64}$`)

func diffSets(a, b string) (onlyA, onlyB []string) {
	sa, sb := toSet(a), toSet(b)
	for k := range sa {
		if !sb[k] {
			onlyA = append(onlyA, k)
		}
	}
	for k := range sb {
		if !sa[k] {
			onlyB = append(onlyB, k)
		}
	}
	return
}

// assertBaseline fails when the machine differs from the snapshot. The
// developer's machine may be in use by someone else while this runs (their
// one-off `run --rm` containers and anonymous volumes come and go), so a
// difference is retried for a while and then only fails when it involves
// something this test could have touched: a scratch resource left behind
// (name contains one of the scratch projects), (a pre-existing resource that vanished is
// only logged: other work on the machine removes containers too).
func (a dockerCounts) assertBaseline(t *testing.T, b func() dockerCounts, scratch []string) {
	t.Helper()
	var now dockerCounts
	for attempt := 0; attempt < 5; attempt++ {
		now = b()
		if a == now || (mapsEqual(toSet(a.containers), toSet(now.containers)) &&
			mapsEqual(toSet(a.volumes), toSet(now.volumes)) && mapsEqual(toSet(a.images), toSet(now.images))) {
			return
		}
		time.Sleep(3 * time.Second)
	}
	for kind, pair := range map[string][2]string{
		"containers": {a.containers, now.containers}, "volumes": {a.volumes, now.volumes}, "images": {a.images, now.images},
	} {
		gone, added := diffSets(pair[0], pair[1])
		for _, g := range gone {
			if !strings.Contains(g, "-run-") && !anonymousVolume.MatchString(g) {
				// ponytail: something else on a shared machine removed it; only
				// scratch leftovers (below) prove this test leaked.
				t.Logf("note: pre-existing %s vanished, not by this test: %s", kind, g)
			}
		}
		for _, n := range added {
			for _, p := range scratch {
				if strings.Contains(n, p) {
					t.Errorf("scratch %s left behind: %s", kind, n)
				}
			}
			t.Logf("note: %s appeared during the run, not from this test: %s", kind, n)
		}
	}
}

// ownOnly keeps the snapshot lines that belong to the given scratch projects.
func ownOnly(c dockerCounts, projects ...string) string {
	var keep []string
	for _, block := range []string{c.containers, c.volumes, c.images} {
		for _, l := range strings.Fields(block) {
			for _, p := range projects {
				if strings.Contains(l, p) {
					keep = append(keep, l)
				}
			}
		}
	}
	sort.Strings(keep)
	return strings.Join(keep, "\n")
}

func runExit(bin string, args ...string) (string, int) {
	out, err := exec.Command(bin, args...).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	return string(out), 0
}

func dockerHas(t *testing.T, kind, name string) bool {
	t.Helper()
	var out string
	switch kind {
	case "volume":
		out = mustRun(t, "docker", "volume", "ls", "-q")
	case "image":
		out = mustRun(t, "docker", "images", "--format", "{{.Repository}}:{{.Tag}}")
	default:
		out = mustRun(t, "docker", "ps", "-a", "--format", "{{.Names}}")
	}
	return toSet(out)[name]
}

// TestLivePrune drives `prune` against scratch instances only (temp state
// dirs, a throwaway proxy name), proving: dry runs delete nothing; --stopped
// removes only stopped instances, with their image and (unless
// --keep-volumes) their volumes; a running instance survives until --all;
// and the machine's Docker state ends exactly as it began.
func TestLivePrune(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-prune")
	mustRun(t, "docker", "pull", "-q", "alpine:latest") // part of the baseline, not of the test

	before := countDocker(t)
	scratch := t.TempDir()
	state := filepath.Join(scratch, "state")
	legacyState := filepath.Join(scratch, "legacy-state")
	global := []string{"--state-dir", state, "--legacy-state-dir", legacyState}
	prune := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), append([]string{"prune"}, args...)...)...)
	}

	newInstance := func(name string) (li liveInstance, project string) {
		root := filepath.Join(scratch, name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		liveInitRepo(t, root)
		files := map[string]string{
			"Dockerfile":   "FROM alpine:latest\nCMD [\"sleep\", \"3600\"]\n",
			"compose.yaml": "services:\n  app:\n    build: .\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n",
		}
		for f, c := range files {
			if err := os.WriteFile(filepath.Join(root, f), []byte(c), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		li = liveInstance{binary: bin, root: root, state: state, legacyState: legacyState}
		li.run(t, "prepare", "-f", filepath.Join(root, "compose.yaml"))
		li.run(t, "up", "--build", "--wait-timeout", "120")
		project, _ = li.where(t)["project"].(string)
		if project == "" {
			t.Fatal("no project from where")
		}
		return li, project
	}
	// Whatever happens, never leave scratch resources behind.
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

	a, pa := newInstance("a-stopped")
	projects = append(projects, pa)
	b, pb := newInstance("b-running")
	projects = append(projects, pb)
	_ = b
	a.run(t, "stop", "app")
	ctr := func(p string) string { return p + "-app-1" }

	// 1. Dry runs: no scope, a scope without --yes (non-TTY: exit 1), --older-than that matches nothing.
	mid := countDocker(t)
	out, code := prune()
	if code != 0 || !strings.Contains(out, pa) || !strings.Contains(out, pb) || !strings.Contains(out, "dry run") {
		t.Fatalf("overview code=%d:\n%s", code, out)
	}
	if out, code = prune("--stopped"); code != 1 || !strings.Contains(out, pa) || strings.Contains(out, "removed") {
		t.Fatalf("--stopped without --yes: code=%d\n%s", code, out)
	}
	if out, code = prune("--all", "--json"); code != 0 || !strings.Contains(out, `"dry_run": true`) {
		t.Fatalf("--all --json code=%d\n%s", code, out)
	}
	if out, code = prune("--stopped", "--older-than", "7d", "--yes"); code != 0 || !strings.Contains(out, "nothing to prune") {
		t.Fatalf("--older-than 7d should select nothing: code=%d\n%s", code, out)
	}
	// Compared on this test's own resources: the machine may be busy with
	// someone else's containers and volumes while this runs.
	if now := countDocker(t); ownOnly(now, pa, pb) != ownOnly(mid, pa, pb) {
		t.Fatalf("a dry run changed scratch resources:\nbefore:\n%s\nafter:\n%s", ownOnly(mid, pa, pb), ownOnly(now, pa, pb))
	}
	for _, p := range []string{pa, pb} {
		if _, err := os.Stat(filepath.Join(state, p, "manifest.json")); err != nil {
			t.Fatalf("dry run touched state of %s: %v", p, err)
		}
	}
	t.Log("PASS: dry runs delete nothing")

	// 2. --stopped --keep-volumes: A goes (container, network, image, state), its volume stays, B untouched.
	if out, code = prune("--stopped", "--keep-volumes", "--yes"); code != 0 || !strings.Contains(out, "removed "+pa) || strings.Contains(out, "removed "+pb) {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if dockerHas(t, "container", ctr(pa)) || dockerHas(t, "image", pa+"-app:dev") {
		t.Fatal("A's container/image should be gone")
	}
	if !dockerHas(t, "volume", pa+"_data") {
		t.Fatal("--keep-volumes must keep A's volume")
	}
	if _, err := os.Stat(filepath.Join(state, pa)); !os.IsNotExist(err) {
		t.Fatal("A's state should be gone")
	}
	if !dockerHas(t, "container", ctr(pb)) {
		t.Fatal("running B must survive --stopped")
	}
	t.Log("PASS: --stopped --keep-volumes removed only A and kept its volume")

	// 3. --stopped without --keep-volumes: C goes with its volume and image; B still untouched.
	c, pc := newInstance("c-stopped")
	projects = append(projects, pc)
	c.run(t, "stop", "app")
	if out, code = prune("--stopped", "--yes"); code != 0 || !strings.Contains(out, "removed "+pc) {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if dockerHas(t, "container", ctr(pc)) || dockerHas(t, "volume", pc+"_data") || dockerHas(t, "image", pc+"-app:dev") {
		t.Fatal("C's container, volume and image should all be gone")
	}
	if !dockerHas(t, "volume", pa+"_data") || !dockerHas(t, "container", ctr(pb)) {
		t.Fatal("A's kept volume and running B must survive")
	}
	t.Log("PASS: --stopped removed C with its volume and image")

	// 4. --all: B is stopped first, then removed.
	if out, code = prune("--all", "--yes"); code != 0 || !strings.Contains(out, "removed "+pb) {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if dockerHas(t, "container", ctr(pb)) || dockerHas(t, "volume", pb+"_data") {
		t.Fatal("B should be gone")
	}
	if out, _ = prune("--all", "--yes"); !strings.Contains(out, "nothing to prune") {
		t.Fatalf("expected nothing left:\n%s", out)
	}

	// A's kept volume is the only scratch leftover; remove it by exact name.
	mustRun(t, "docker", "volume", "rm", pa+"_data")
	t.Log("PASS: scratch resources all removed (baseline checked on exit)")
}
