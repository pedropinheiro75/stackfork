package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/prune"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// pruneFake is a fake `docker` whose listings come from files, deliberately
// ignoring every --filter: whatever is in the files is "on the machine",
// including objects that are not stackfork's. Only commands that remove or
// stop something matter to the tests, and they are logged.
type pruneFake struct{ dir string }

func installPruneDocker(t *testing.T) *pruneFake {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
D="` + dir + `"
{ for a in "$@"; do printf '%s ' "$a"; done; printf '\n'; } >> "$D/log"
[ -f "$D/failarg" ] && case " $* " in *" $(cat "$D/failarg") "*) echo boom >&2; exit 1;; esac
case "$1" in
  context) printf '%s' '[{"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock"}}}]' ;;
  ps) cat "$D/ps" 2>/dev/null ;;
  network) [ "$2" = ls ] && cat "$D/network" 2>/dev/null ;;
  volume) [ "$2" = ls ] && cat "$D/volume" 2>/dev/null ;;
  image) [ "$2" = ls ] && cat "$D/image" 2>/dev/null ;;
  system) cat "$D/df" 2>/dev/null ;;
  inspect) case " $* " in *FinishedAt*) cat "$D/inspect" 2>/dev/null ;; *) printf false ;; esac ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STACKFORK_PROXY_NAME", "test-proxy")
	return &pruneFake{dir: dir}
}

func (f *pruneFake) set(t *testing.T, name string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *pruneFake) log(t *testing.T) string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "log"))
	return string(b)
}

// removals is the log restricted to commands that delete or stop something.
func (f *pruneFake) removals(t *testing.T) []string {
	var out []string
	for _, l := range strings.Split(f.log(t), "\n") {
		w := strings.Fields(l)
		if len(w) == 0 {
			continue
		}
		if w[0] == "rm" || w[0] == "stop" || (len(w) > 1 && w[1] == "rm") {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

func psRow(id, name, st, project string, labels ...string) string {
	l := append([]string{"io.stackfork.root=/r", "com.docker.compose.project=" + project}, labels...)
	return fmt.Sprintf(`{"ID":%q,"Names":%q,"State":%q,"Labels":%q}`, id, name, st, strings.Join(l, ","))
}

func netRow(name, project string) string {
	return fmt.Sprintf(`{"ID":"n-%s","Name":%q,"Labels":"com.docker.compose.project=%s"}`, name, name, project)
}
func volRow(name, project string) string {
	return fmt.Sprintf(`{"Name":%q,"Labels":"com.docker.compose.project=%s"}`, name, project)
}
func imgRow(ref string) string {
	repo, tag, _ := strings.Cut(ref, ":")
	return fmt.Sprintf(`{"Repository":%q,"Tag":%q,"Size":"10MB"}`, repo, tag)
}

// pruneInstance writes an instance (new or legacy namespace) whose checkout
// exists (a git repo on `main`) unless orphan is true. Its snapshot has a
// built service `web` and a pulled `db`.
func pruneInstance(t *testing.T, base string, legacy bool, name string, orphan bool) (project, root string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), name)
	if !orphan {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "init", "-q", "-b", "main", root).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	var err error
	if legacy {
		project, err = identity.LegacyProjectName(root, "main")
	} else {
		project, err = identity.ProjectName(root, "main")
	}
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "compose.json")
	snap := fmt.Sprintf(`{"name":%q,"services":{"web":{"image":"%s-web:dev","build":{"context":"."}},"db":{"image":"postgres:17"}}}`, project, project)
	if err := os.WriteFile(cfg, []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, _ := state.SHA256File(cfg)
	host, _ := identity.BrowserHostname(project, "")
	m := &state.Manifest{Project: project, Root: root, Branch: "main", Hostname: host,
		Files: []string{cfg}, Inputs: map[string]string{}, ConfigSHA256: hash}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	return project, root
}

func runPrune(t *testing.T, base, legacy string, args ...string) (string, int, error) {
	t.Helper()
	var out bytes.Buffer
	code, err := cmdPrune(&out, &out, args, base, legacy)
	return out.String(), code, err
}

// machine builds: stopped (sf), running (sf), orphan (idv legacy), plus
// foreign objects that must survive everything.
type machine struct {
	f                        *pruneFake
	base, legacy             string
	stopped, running, orphan string
}

func newMachine(t *testing.T) machine {
	t.Helper()
	f := installPruneDocker(t)
	m := machine{f: f, base: t.TempDir(), legacy: t.TempDir()}
	m.stopped, _ = pruneInstance(t, m.base, false, "stopped-app", false)
	m.running, _ = pruneInstance(t, m.base, false, "running-app", false)
	m.orphan, _ = pruneInstance(t, m.legacy, true, "gone-app", true)
	f.set(t, "ps",
		psRow("c-stopped", "stopped-web", "exited", m.stopped),
		psRow("c-running", "running-web", "running", m.running),
		psRow("c-orphan", "orphan-web", "exited", m.orphan),
		// foreign: no stackfork label but a stackfork-looking project name
		`{"ID":"c-foreign1","Names":"sf-lookalike","State":"exited","Labels":"com.docker.compose.project=`+m.stopped+`"}`,
		// foreign: stackfork label, project that is no known instance
		psRow("c-foreign2", "sf-other", "exited", "sf-other-project-123456789abc"),
		// foreign: unrelated project
		`{"ID":"c-foreign3","Names":"company-brain","State":"running","Labels":"com.docker.compose.project=company-brain"}`,
		`{"ID":"c-proxy","Names":"test-proxy","State":"running","Labels":"io.stackfork.proxy=true"}`,
		// an unrelated container that is merely named like the real proxy
		`{"ID":"c-notproxy","Names":"stackfork-proxy","State":"running","Labels":""}`,
	)
	f.set(t, "network", netRow(m.stopped+"_default", m.stopped), netRow(m.running+"_default", m.running),
		netRow(m.orphan+"_default", m.orphan), netRow("company-brain_default", "company-brain"),
		netRow("k3d-company", ""), `{"ID":"n-bridge","Name":"bridge","Labels":""}`)
	f.set(t, "volume", volRow(m.stopped+"_pgdata", m.stopped), volRow(m.orphan+"_pgdata", m.orphan),
		volRow(m.running+"_pgdata", m.running), volRow("company-brain_data", "company-brain"),
		volRow(m.stopped+"-lookalike", "someone-else"), `{"Name":"anon123","Labels":"com.docker.volume.anonymous="}`)
	f.set(t, "image", imgRow(m.stopped+"-web:dev"), imgRow(m.running+"-web:dev"), imgRow(m.orphan+"-web:dev"),
		imgRow("postgres:17"), imgRow("sf-unrelated-web:dev"), imgRow(m.stopped+"-web:latest"), imgRow("caddy:2-alpine"))
	f.set(t, "df", fmt.Sprintf(`{"Images":[{"Repository":%q,"Tag":"dev","Size":"200MB"}],"Volumes":[{"Name":%q,"Size":"1.5GB"}]}`,
		m.stopped+"-web", m.stopped+"_pgdata"))
	return m
}

func hasRemoval(rm []string, word string) bool {
	for _, l := range rm {
		if strings.Contains(" "+l+" ", " "+word+" ") {
			return true
		}
	}
	return false
}

func TestPruneDryRunDeletesNothing(t *testing.T) {
	m := newMachine(t)
	for _, args := range [][]string{nil, {"--stopped"}, {"--all"}, {"--orphaned", "--older-than", "1h"}, {"--json"}} {
		out, code, err := runPrune(t, m.base, m.legacy, args...)
		want := 0
		if len(args) > 0 && args[0] != "--json" && args[0] != "--orphaned" { // a scope without a TTY or --yes
			want = 1
		}
		if err != nil || code != want {
			t.Fatalf("%v: code=%d err=%v\n%s", args, code, err, out)
		}
		if rm := m.f.removals(t); len(rm) != 0 {
			t.Fatalf("%v: dry run removed things: %v", args, rm)
		}
	}
	out, _, _ := runPrune(t, m.base, m.legacy)
	for _, want := range []string{m.stopped, m.running, m.orphan, "legacy", "--orphaned", "--stopped", "--all",
		"1.5 GB", "(running)"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "company") {
		t.Errorf("foreign project leaked into the plan:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(m.base, m.stopped, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestPruneYesNeedsScope(t *testing.T) {
	m := newMachine(t)
	_, code, err := runPrune(t, m.base, m.legacy, "--yes")
	if code != 2 || err == nil || len(m.f.removals(t)) != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

// Everything removed must be an exact, labelled member of a known instance.
func TestPruneStoppedRemovesOnlyOwnedStoppedResources(t *testing.T) {
	m := newMachine(t)
	out, code, err := runPrune(t, m.base, m.legacy, "--stopped", "--yes")
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out)
	}
	rm := m.f.removals(t)
	wantOnly := []string{
		"rm -f c-stopped", "rm -f c-orphan",
		"network rm " + m.stopped + "_default", "network rm " + m.orphan + "_default",
		"volume rm " + m.stopped + "_pgdata", "volume rm " + m.orphan + "_pgdata",
		"image rm " + m.stopped + "-web:dev", "image rm " + m.orphan + "-web:dev",
	}
	if len(rm) != len(wantOnly) {
		t.Fatalf("removals:\n%s\nwant %d", strings.Join(rm, "\n"), len(wantOnly))
	}
	for _, w := range wantOnly {
		found := false
		for _, l := range rm {
			found = found || l == w
		}
		if !found {
			t.Errorf("missing removal %q in:\n%s", w, strings.Join(rm, "\n"))
		}
	}
	for _, bad := range []string{"c-running", "c-foreign1", "c-foreign2", "c-foreign3", "c-proxy", "c-notproxy",
		"company", "k3d", "bridge", "anon123", "someone-else", "postgres", "caddy", "sf-unrelated", m.running, ":latest"} {
		for _, l := range rm {
			if strings.Contains(l, bad) {
				t.Errorf("foreign/running object touched (%q): %s", bad, l)
			}
		}
	}
	if hasRemoval(rm, "stop") {
		t.Error("nothing running was selected, nothing should be stopped")
	}
	for _, p := range []string{filepath.Join(m.base, m.stopped), filepath.Join(m.legacy, m.orphan)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("state dir %s should be gone", p)
		}
	}
	if _, err := os.Stat(filepath.Join(m.base, m.running)); err != nil {
		t.Errorf("running instance's state must survive: %v", err)
	}
	if !strings.Contains(out, "removed "+m.stopped) || !strings.Contains(out, "removed "+m.orphan) {
		t.Errorf("output:\n%s", out)
	}
	if strings.Contains(m.f.log(t), "system prune") {
		t.Error("never docker system prune")
	}
}

func TestPruneKeepVolumes(t *testing.T) {
	m := newMachine(t)
	if _, code, err := runPrune(t, m.base, m.legacy, "--stopped", "--keep-volumes", "--yes"); err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if hasRemoval(m.f.removals(t), "volume") {
		t.Fatalf("volumes removed: %v", m.f.removals(t))
	}
	if !hasRemoval(m.f.removals(t), "c-stopped") {
		t.Fatal("containers should still go")
	}
}

func TestPruneOrphanedSkipsRunningOrphan(t *testing.T) {
	m := newMachine(t)
	m.f.set(t, "ps", psRow("c-orphan", "o", "running", m.orphan))
	out, _, err := runPrune(t, m.base, m.legacy, "--orphaned", "--yes")
	if err != nil || len(m.f.removals(t)) != 0 || !strings.Contains(out, "nothing to prune") {
		t.Fatalf("err=%v rm=%v\n%s", err, m.f.removals(t), out)
	}
}

func TestPruneAllStopsRunningFirstAndRemovesProxy(t *testing.T) {
	m := newMachine(t)
	out, code, err := runPrune(t, m.base, m.legacy, "--all", "--yes")
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out)
	}
	rm := m.f.removals(t)
	stopAt, rmAt := -1, -1
	for i, l := range rm {
		if l == "stop c-running" {
			stopAt = i
		}
		if l == "rm -f c-running" {
			rmAt = i
		}
	}
	if stopAt < 0 || rmAt < 0 || stopAt > rmAt {
		t.Fatalf("running instance must be stopped before removal:\n%s", strings.Join(rm, "\n"))
	}
	if !hasRemoval(rm, "c-proxy") {
		t.Fatal("--all removes the stackfork proxy container")
	}
	for _, l := range rm {
		if strings.Contains(l, "c-notproxy") || strings.Contains(l, "c-foreign") {
			t.Errorf("foreign container touched: %s", l)
		}
	}
}

func TestPruneNonTTYWithoutYesExits1(t *testing.T) {
	m := newMachine(t)
	out, code, err := runPrune(t, m.base, m.legacy, "--stopped")
	if err != nil || code != 1 || !strings.Contains(out, "--yes") || len(m.f.removals(t)) != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out)
	}
}

func TestPruneInteractiveConfirmation(t *testing.T) {
	cases := []struct {
		name, input string
		scope       pruneScopeName
		want        bool
	}{
		{"y for stopped", "y\n", "stopped", true},
		{"yes for stopped", "yes\n", "stopped", true},
		{"n", "n\n", "stopped", false},
		{"empty", "\n", "stopped", false},
		{"eof", "", "stopped", false},
		{"y not enough for running", "y\n", "all", false},
		{"word for running", "prune\n", "all", true},
		{"word for all", "prune\n", "all", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMachine(t)
			var out bytes.Buffer
			r := pruneRun{scopes: scopeOf(c.scope), newBase: m.base, legacyBase: m.legacy,
				stdin: strings.NewReader(c.input), isTTY: true, now: time.Now()}
			code, err := r.run(&out, &out)
			if err != nil {
				t.Fatal(err)
			}
			got := len(m.f.removals(t)) > 0
			if got != c.want || (c.want && code != 0) || (!c.want && code != 1) {
				t.Fatalf("removed=%v code=%d\n%s", got, code, out.String())
			}
		})
	}
}

func TestPruneOlderThan(t *testing.T) {
	m := newMachine(t)
	old := time.Now().Add(-10 * 24 * time.Hour)
	for _, p := range []string{filepath.Join(m.base, m.stopped), filepath.Join(m.base, m.stopped, "manifest.json")} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// the orphan's container finished recently, so it is not old
	m.f.set(t, "inspect", fmt.Sprintf("c-orphan 2026-01-01T00:00:00Z %s", time.Now().UTC().Format(time.RFC3339Nano)))
	out, code, err := runPrune(t, m.base, m.legacy, "--stopped", "--older-than", "7d", "--yes")
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out)
	}
	rm := m.f.removals(t)
	if !hasRemoval(rm, "c-stopped") || hasRemoval(rm, "c-orphan") || hasRemoval(rm, "c-running") {
		t.Fatalf("older-than selection wrong:\n%s", strings.Join(rm, "\n"))
	}
	if _, _, err := runPrune(t, m.base, m.legacy, "--older-than", "soon"); err == nil {
		t.Fatal("bad duration should fail")
	}
}

func TestPruneFailureKeepsStateAndExits1(t *testing.T) {
	m := newMachine(t)
	if err := os.WriteFile(filepath.Join(m.f.dir, "failarg"), []byte(m.stopped+"_pgdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code, err := runPrune(t, m.base, m.legacy, "--stopped", "--yes")
	if err != nil || code != 1 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out)
	}
	if _, err := os.Stat(filepath.Join(m.base, m.stopped)); err != nil {
		t.Error("state of a failed instance must be kept")
	}
	if _, err := os.Stat(filepath.Join(m.legacy, m.orphan)); !os.IsNotExist(err) {
		t.Error("the other instance should still be removed")
	}
}

func TestPruneJSON(t *testing.T) {
	m := newMachine(t)
	out, code, err := runPrune(t, m.base, m.legacy, "--stopped", "--json")
	if err != nil || code != 0 || !strings.Contains(out, `"dry_run": true`) || len(m.f.removals(t)) != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out)
	}
}

type pruneScopeName string

func scopeOf(s pruneScopeName) (sc prune.Scopes) {
	switch s {
	case "stopped":
		sc.Stopped = true
	case "all":
		sc.All = true
	}
	return
}
