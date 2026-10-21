package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/live"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/runs"
)

// TestLiveMode: with real Docker and git, a push to the bare origin that
// changes the lockfile and adds a migration gives one fast-forward and one
// succeeded live run (the image rebuilt, the migration run once, the data
// volume and its content kept); a push that breaks the Dockerfile gives one
// failed run and a pause. Temp repositories, a temp bare origin, a temp state
// dir and a test proxy name; alpine:latest is already on the machine, nothing
// is pulled. The built image and the volume are removed at the end.
func TestLiveMode(t *testing.T) {
	liveEnabled(t)
	bin := stackforkBinary(t)
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-livetest-mode")
	t.Setenv("STACKFORK_CONFIG_DIR", t.TempDir())

	before := countDocker(t)
	scratch, _ := filepath.EvalSymlinks(t.TempDir()) // the watch set compares resolved paths
	stateDir, legacy := filepath.Join(scratch, "state"), filepath.Join(scratch, "legacy")
	global := []string{"--state-dir", stateDir, "--legacy-state-dir", legacy}
	sf := func(args ...string) (string, int) {
		return runExit(bin, append(append([]string{}, global...), args...)...)
	}
	gitc := func(dir string, args ...string) string {
		return mustRun(t, "git", append([]string{"-C", dir, "-c", "user.email=t@e.x", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	}

	// origin, the repository under test and a second clone that pushes
	origin := filepath.Join(scratch, "origin.git")
	mustRun(t, "git", "init", "-q", "--bare", "-b", "main", origin)
	repo := filepath.Join(scratch, "livemode")
	os.MkdirAll(repo, 0o755)
	mustRun(t, "git", "init", "-q", "-b", "main", repo)
	put := func(dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(repo, "docker-compose.yml", "services:\n  app:\n    build:\n      context: .\n      dockerfile: Dockerfile\n    command: [\"sleep\", \"3600\"]\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n")
	put(repo, "Dockerfile", "FROM alpine:latest\nCOPY bun.lock /bun.lock\n")
	put(repo, "bun.lock", "v1\n")
	put(repo, "prisma/migrations/1_init/migration.sql", "select 1;\n")
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Facts: knowledge.Facts{PackageManager: "bun", MigrationDirs: []string{"prisma/migrations"}},
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
			{ID: "prepare", Kind: knowledge.KindPrepare, Args: []string{"-f", "docker-compose.yml"}},
			{ID: "up", Kind: knowledge.KindComposeUp},
			{ID: "migrate", Kind: knowledge.KindExec, Once: knowledge.OnceSchema, Service: "app", Cmd: "echo migrated >> /data/migrations.log"},
		}}}
	if err := k.Save(filepath.Join(repo, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "add", "-A")
	gitc(repo, "commit", "-q", "-m", "init")
	gitc(repo, "remote", "add", "origin", origin)
	gitc(repo, "push", "-q", "-u", "origin", "main")
	pusher := filepath.Join(scratch, "pusher")
	mustRun(t, "git", "clone", "-q", origin, pusher)

	out, _ := sf("--root", repo, "where")
	i := strings.Index(out, `"project": "`)
	j := strings.Index(out[i+12:], `"`)
	p := out[i+12 : i+12+j]
	instanceDir := filepath.Join(stateDir, p)
	defer func() {
		sf("--root", repo, "down", "--volumes")
		exec.Command("docker", "rm", "-f", p+"-app-1", "sf-proxy-livetest-mode").Run()
		exec.Command("docker", "volume", "rm", p+"_data").Run()
		exec.Command("docker", "network", "rm", p+"_default").Run()
		exec.Command("docker", "rmi", p+"-app:dev").Run()
		before.assertBaseline(t, func() dockerCounts { return countDocker(t) }, []string{p})
	}()

	if out, code := sf("start-env", repo); code != 0 {
		t.Fatalf("start-env: %s", out)
	}
	if out, code := sf("--root", repo, "exec", "app", "sh", "-c", "touch /data/marker"); code != 0 {
		t.Fatalf("marker: %s", out)
	}
	imageOf := func() string {
		return strings.TrimSpace(mustRun(t, "docker", "inspect", "-f", "{{.Image}}", p+"-app-1"))
	}
	image0 := imageOf()

	// the loop's pieces: watch set, poller, baseline, reconciler
	compose, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := live.WatchSet(live.Inputs{Checkout: repo, ComposeJSON: compose, PackageManager: "bun",
		ComposeFiles: []string{"docker-compose.yml"}, MigrationDirs: k.Facts.MigrationDirs})
	if err != nil {
		t.Fatal(err)
	}
	poller := live.NewPoller(repo, entries, nil)
	poller.Poll()
	bl, created, err := live.InitBaseline(instanceDir, "main", "", poller, k.Plan)
	if err != nil || !created {
		t.Fatalf("baseline: %v %v", created, err)
	}
	poller.SetBaseline(bl.Digests)
	if pend := poller.Poll(); len(pend) != 0 {
		t.Fatalf("nothing changed yet: %+v", pend)
	}
	now := time.Now()
	rec := &live.Reconciler{Now: func() time.Time { return now }, Exec: LiveExec(stateDir, legacy, nil, nil)}
	target := func(changes []live.Change, up *runs.CauseUpstream) live.Target {
		t.Helper()
		ts, err := live.Targets(stateDir, legacy, repo, Version)
		if err != nil || len(ts) != 1 {
			t.Fatalf("targets: %+v %v", ts, err)
		}
		ts[0].Changes, ts[0].Upstream = changes, up
		return ts[0]
	}
	push := func(msg string, edit func()) *runs.CauseUpstream {
		t.Helper()
		edit()
		gitc(pusher, "add", "-A")
		gitc(pusher, "commit", "-q", "-m", msg)
		gitc(pusher, "push", "-q", "origin", "main")
		old := strings.TrimSpace(gitc(repo, "rev-parse", "HEAD"))
		res := repos.Sync(repo, 30*time.Second, true)
		if !res.Updated || res.Behind != 1 {
			t.Fatalf("expected one fast-forward: %+v", res)
		}
		return &runs.CauseUpstream{Ref: res.Upstream, From: old[:7], To: res.UpstreamSHA[:7], Commits: res.Behind}
	}

	// 1. a lockfile change and a new migration: one fast-forward, one succeeded live run
	up := push("bun.lock and a migration", func() {
		put(pusher, "bun.lock", "v2\n")
		put(pusher, "prisma/migrations/2_add_invoices/migration.sql", "select 2;\n")
	})
	changes := poller.Poll()
	cats := map[string]string{}
	for _, c := range changes {
		cats[c.Path] = c.Category + ":" + c.Service
	}
	if len(changes) != 2 || cats["bun.lock"] != "build:app" || cats["prisma/migrations/2_add_invoices/migration.sql"] != "migrations:" {
		t.Fatalf("changes: %+v", changes)
	}
	o, err := rec.Reconcile(context.Background(), target(changes, up))
	if err != nil || o.Status != live.Ran || o.Run.Outcome != runs.Succeeded {
		t.Fatalf("reconcile: %+v %v", o, err)
	}
	r, err := runs.Get(instanceDir, o.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, s := range r.Steps {
		states = append(states, s.ID+"="+s.State)
	}
	if r.Command != "live" || r.Trigger != "live" || r.Cause == nil || r.Cause.Upstream.Commits != 1 ||
		!strings.HasPrefix(strings.Join(states, " "), "secrets=skipped rebuild=succeeded") || !strings.HasSuffix(strings.Join(states, " "), "migrate=succeeded") {
		t.Fatalf("%s/%s cause %+v %v", r.Command, r.Trigger, r.Cause, states)
	}
	if imageOf() == image0 {
		t.Error("the changed lockfile must rebuild the image")
	}
	if out, code := sf("--root", repo, "exec", "app", "ls", "/data/marker"); code != 0 {
		t.Fatalf("the data volume lost its content: %s", out)
	}
	if out, _ := sf("--root", repo, "exec", "app", "cat", "/data/migrations.log"); strings.Count(out, "migrated") != 2 {
		t.Fatalf("the migration must have run once more (start + live), got: %s", out)
	}
	bl2, _ := live.LoadBaseline(instanceDir)
	if bl2 == nil || bl2.Digests["bun.lock"] == "" || bl2.Upstream != up.To {
		t.Fatalf("baseline after the run: %+v", bl2)
	}
	poller.SetBaseline(bl2.Digests) // the loop reloads the poller's baseline after a succeeded run

	// 2. a commit that breaks the Dockerfile: one failed run, then a pause
	now = now.Add(time.Minute)
	up = push("break the Dockerfile", func() { put(pusher, "Dockerfile", "FROM alpine:latest\nRUN exit 1\n") })
	changes = poller.Poll()
	if len(changes) != 1 || changes[0].Path != "Dockerfile" {
		t.Fatalf("changes: %+v", changes)
	}
	o, err = rec.Reconcile(context.Background(), target(changes, up))
	if err != nil || o.Status != live.Ran || o.Run.Outcome != runs.Failed || o.Pause == nil || o.Pause.Reason != "run "+strconv.Itoa(o.Run.ID)+" failed at step rebuild" {
		t.Fatalf("failed reconcile: %+v %v", o, err)
	}
	for i := 0; i < 20; i++ { // ten simulated minutes
		now = now.Add(30 * time.Second)
		if o2, _ := rec.Reconcile(context.Background(), target(changes, nil)); o2.Status != live.Paused {
			t.Fatalf("tick %d: %+v", i, o2)
		}
	}
	list, _ := runs.List(instanceDir)
	var liveRuns, failed int
	for _, x := range list {
		if x.Command == "live" {
			liveRuns++
			if x.Outcome == runs.Failed {
				failed++
			}
		}
	}
	if liveRuns != 2 || failed != 1 {
		t.Fatalf("%d live runs, %d failed", liveRuns, failed)
	}

	// every volume that existed still exists, and so does the instance's
	after := countDocker(t)
	for v := range toSet(before.volumes) {
		if !toSet(after.volumes)[v] {
			t.Errorf("volume %s is gone", v)
		}
	}
	if !toSet(after.volumes)[p+"_data"] {
		t.Errorf("the instance's volume %s_data is gone", p)
	}
}
