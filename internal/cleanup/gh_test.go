package cleanup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGH writes a gh script that logs "args" and (for `pr list`) the env,
// answers `auth status` with authExit and `pr list` with the JSON in pr.json.
type fakeGH struct{ Bin, Log, JSON, Auth string }

func newFakeGH(t *testing.T) fakeGH {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := tmp(t)
	f := fakeGH{Bin: filepath.Join(dir, "gh"), Log: filepath.Join(dir, "log"), JSON: filepath.Join(dir, "pr.json"), Auth: filepath.Join(dir, "auth")}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %[1]s
case "$1" in
  auth) [ -e %[3]s ] && exit 1; exit 0;;
  pr) env | sort >> %[1]s.env; cat %[2]s;;
esac
`, f.Log, f.JSON, f.Auth)
	if err := os.WriteFile(f.Bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o755)
	f.setPR(t, `[{"number":42,"state":"MERGED","url":"https://example.com/acme-web/pull/42","mergedAt":"2026-10-19T10:00:00Z"}]`)
	return f
}

func (f fakeGH) setPR(t *testing.T, j string) {
	t.Helper()
	os.WriteFile(f.JSON, []byte(j), 0o644)
}

func (f fakeGH) calls() []string {
	b, _ := os.ReadFile(f.Log)
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (f fakeGH) count(prefix string) int {
	n := 0
	for _, c := range f.calls() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newGH(f fakeGH) (*GH, *clock) {
	c := &clock{time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	return &GH{Paths: []string{f.Bin}, Now: c.now}, c
}

func merged(b string) Result { return Result{Branch: b, State: Merged, Via: ViaPatchID} }

func TestGHOnPathNeverRuns(t *testing.T) {
	onPath := newFakeGH(t)
	t.Setenv("PATH", filepath.Dir(onPath.Bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	g := &GH{Paths: []string{filepath.Join(tmp(t), "no-gh")}}
	r := g.Enrich(tmp(t), "abc", merged("feature-login"))
	if r.PR != 0 || r.State != Merged || len(onPath.calls()) != 0 {
		t.Fatalf("%+v, calls %v", r, onPath.calls())
	}
}

func TestGHEnrichesWithFixedPath(t *testing.T) {
	f := newFakeGH(t)
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "must-not-pass")
	t.Setenv("GH_HOST", "example.com")
	g, _ := newGH(f)
	r := g.Enrich(tmp(t), "abc", merged("feature-login"))
	if r.PR != 42 || r.State != Merged || r.Forge != "MERGED" || !strings.HasSuffix(r.URL, "/pull/42") {
		t.Fatalf("%+v", r)
	}
	if got := f.calls(); len(got) != 2 || got[0] != "auth status" || !strings.HasPrefix(got[1], "pr list --head feature-login --state all --limit 1 --json number,state,url,mergedAt") {
		t.Fatalf("calls %q", got)
	}
	env, _ := os.ReadFile(f.Log + ".env")
	if !strings.Contains(string(env), "PATH=/usr/bin:/bin") || !strings.Contains(string(env), "GH_HOST=example.com") || strings.Contains(string(env), "OP_SERVICE") {
		t.Fatalf("env:\n%s", env)
	}
}

func TestGHCacheAndAuthInterval(t *testing.T) {
	f := newFakeGH(t)
	g, c := newGH(f)
	for i := 0; i < 3; i++ {
		g.Enrich(tmp(t), "abc", merged("feature-login"))
	}
	if f.count("pr list") != 1 || f.count("auth") != 1 {
		t.Fatalf("calls %q", f.calls())
	}
	g.Enrich(tmp(t), "def", merged("feature-login")) // new head commit
	if f.count("pr list") != 2 {
		t.Fatalf("new head not asked: %q", f.calls())
	}
	c.t = c.t.Add(6*time.Hour + time.Minute)
	g.Enrich(tmp(t), "abc", merged("feature-login"))
	if f.count("pr list") != 3 || f.count("auth") != 2 {
		t.Fatalf("after 6 h: %q", f.calls())
	}
}

func TestGHAuthFailureTurnsEnrichmentOffUntilTheNextCheck(t *testing.T) {
	f := newFakeGH(t)
	os.WriteFile(f.Auth, nil, 0o644) // auth status exits 1
	g, c := newGH(f)
	r := g.Enrich(tmp(t), "abc", merged("a"))
	g.Enrich(tmp(t), "abc", merged("b"))
	if r.PR != 0 || f.count("pr list") != 0 || f.count("auth") != 1 {
		t.Fatalf("calls %q", f.calls())
	}
	os.Remove(f.Auth)
	c.t = c.t.Add(time.Hour)
	if r := g.Enrich(tmp(t), "abc", merged("a")); r.PR != 42 {
		t.Fatalf("not back on after the hourly check: %+v", r)
	}
}

func TestGHHourlyCap(t *testing.T) {
	f := newFakeGH(t)
	g, c := newGH(f)
	for i := 0; i < 40; i++ {
		g.Enrich(tmp(t), "abc", merged(fmt.Sprint("b", i)))
	}
	if n := len(f.calls()); n != ghMaxCalls {
		t.Fatalf("%d calls, want %d", n, ghMaxCalls)
	}
	c.t = c.t.Add(time.Hour + time.Second)
	g.Enrich(tmp(t), "abc", merged("later"))
	if n := len(f.calls()); n <= ghMaxCalls {
		t.Fatal("no calls after the hour passed")
	}
}

func TestGHDowngradesButNeverUpgrades(t *testing.T) {
	f := newFakeGH(t)
	g, _ := newGH(f)
	f.setPR(t, `[{"number":7,"state":"CLOSED","url":"u","mergedAt":null}]`)
	if r := g.Enrich(tmp(t), "1", merged("a")); r.State != Closed || r.Via != "" || r.PR != 7 {
		t.Fatalf("CLOSED did not downgrade: %+v", r)
	}
	f.setPR(t, `[{"number":8,"state":"MERGED","url":"u","mergedAt":"x"}]`)
	for _, st := range []string{Closed, Open} {
		n := f.count("pr list")
		r := g.Enrich(tmp(t), "2", Result{Branch: "b-" + st, State: st})
		if r.State != st {
			t.Fatalf("MERGED upgraded %s to %s", st, r.State)
		}
		if st == Open && f.count("pr list") != n {
			t.Fatal("gh was asked about an open branch")
		}
	}
	if r := g.Enrich(tmp(t), "3", merged("c")); r.State != Merged || r.PR != 8 {
		t.Fatalf("%+v", r)
	}
}

func TestGHNoPullRequestLeavesTheResult(t *testing.T) {
	f := newFakeGH(t)
	f.setPR(t, `[]`)
	g, _ := newGH(f)
	if r := g.Enrich(tmp(t), "abc", merged("a")); r.State != Merged || r.PR != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestGHRefusesAnInsecureBinary(t *testing.T) {
	f := newFakeGH(t)
	os.Chmod(f.Bin, 0o775) // group writable
	g, _ := newGH(f)
	if r := g.Enrich(tmp(t), "abc", merged("a")); r.PR != 0 || len(f.calls()) != 0 {
		t.Fatalf("ran a group-writable gh: %+v %q", r, f.calls())
	}
	os.Chmod(f.Bin, 0o755)
	g, _ = newGH(f)
	g.RefuseUnder = []string{filepath.Dir(f.Bin)} // inside a scanned root
	if r := g.Enrich(tmp(t), "abc", merged("a")); r.PR != 0 || len(f.calls()) != 0 {
		t.Fatalf("ran a gh inside a repository root: %+v", r)
	}
}

func TestGHRefusesASymlinkToAWritableLocation(t *testing.T) {
	f := newFakeGH(t)
	dir := tmp(t)
	os.Chmod(dir, 0o755)
	link := filepath.Join(dir, "gh")
	os.Symlink(f.Bin, link)
	os.Chmod(filepath.Dir(f.Bin), 0o777)
	g := &GH{Paths: []string{link}}
	if r := g.Enrich(tmp(t), "abc", merged("a")); r.PR != 0 || len(f.calls()) != 0 {
		t.Fatalf("followed a link into a world-writable directory: %+v", r)
	}
}
