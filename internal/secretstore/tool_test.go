package secretstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveToolAcceptsPrivateBinary(t *testing.T) {
	p := newHelper(t, helperCfg{})
	got, err := resolveTool([]string{filepath.Join(t.TempDir(), "absent"), p}, nil)
	if err != nil || got != p {
		t.Fatalf("%q %v", got, err)
	}
}

func TestResolveToolFirstExistingWinsAndMissingIsNotInstalled(t *testing.T) {
	if _, err := resolveTool([]string{"/nonexistent/op"}, nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
	skipNoModes(t)
	bad := newHelper(t, helperCfg{})
	os.Chmod(bad, 0o777)
	good := newHelper(t, helperCfg{})
	// the first existing candidate decides: a refused op is not skipped for a later one.
	if _, err := resolveTool([]string{bad, good}, nil); err == nil {
		t.Fatal("fell through to a later candidate")
	}
}

func TestResolveToolRefusals(t *testing.T) {
	t.Run("world-writable file", func(t *testing.T) {
		skipNoModes(t)
		p := newHelper(t, helperCfg{})
		os.Chmod(p, 0o757)
		if _, err := resolveTool([]string{p}, nil); err == nil || !strings.Contains(err.Error(), "file is writable") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("group-writable file", func(t *testing.T) {
		skipNoModes(t)
		p := newHelper(t, helperCfg{})
		os.Chmod(p, 0o775)
		if _, err := resolveTool([]string{p}, nil); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("group-writable directory", func(t *testing.T) {
		skipNoModes(t)
		p := newHelper(t, helperCfg{})
		os.Chmod(filepath.Dir(p), 0o775)
		if _, err := resolveTool([]string{p}, nil); err == nil || !strings.Contains(err.Error(), "directory is writable") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("not executable", func(t *testing.T) {
		skipNoModes(t)
		p := newHelper(t, helperCfg{})
		os.Chmod(p, 0o600)
		if _, err := resolveTool([]string{p}, nil); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("foreign owner", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("needs root to create a file owned by another user")
		}
		p := newHelper(t, helperCfg{})
		os.Chown(p, 12345, 12345)
		if _, err := resolveTool([]string{p}, nil); err == nil || !strings.Contains(err.Error(), "owner") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("inside a scanned root", func(t *testing.T) {
		p := newHelper(t, helperCfg{})
		root := filepath.Dir(filepath.Dir(p))
		if _, err := resolveTool([]string{p}, []string{root}); err == nil || !strings.Contains(err.Error(), "scanned repository root") {
			t.Fatalf("%v", err)
		}
		if _, err := resolveTool([]string{p}, []string{t.TempDir()}); err != nil {
			t.Fatalf("unrelated root refused it: %v", err)
		}
	})
	t.Run("symlink into a scanned root", func(t *testing.T) {
		target := newHelper(t, helperCfg{})
		link := filepath.Join(t.TempDir(), "op")
		if err := os.Symlink(target, link); err != nil {
			t.Skip(err)
		}
		if _, err := resolveTool([]string{link}, nil); err != nil {
			t.Fatalf("symlink to a good binary refused: %v", err)
		}
		if _, err := resolveTool([]string{link}, []string{filepath.Dir(target)}); err == nil {
			t.Fatal("link out of a root hid a binary inside it")
		}
	})
	t.Run("symlink to a world-writable file", func(t *testing.T) {
		skipNoModes(t)
		target := newHelper(t, helperCfg{})
		os.Chmod(target, 0o777)
		link := filepath.Join(t.TempDir(), "op")
		os.Symlink(target, link)
		if _, err := resolveTool([]string{link}, nil); err == nil {
			t.Fatal("followed a link to an unsafe file")
		}
	})
}

func TestToolEnvIsMinimal(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	t.Setenv("OP_SERVICE_ACCOUNT_TOKEN", "canary-op")
	t.Setenv("OPXTRA", "no")
	t.Setenv("NPM_TOKEN", "canary-npm")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/x")
	t.Setenv("PATH", "/evil")
	has := func(env []string, kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	op := toolEnv("linux", true)
	if !has(op, "OP_SERVICE_ACCOUNT_TOKEN=canary-op") || !has(op, "PATH=/usr/bin:/bin") || !has(op, "HOME=/home/x") {
		t.Fatalf("op env: %v", op)
	}
	for _, e := range op {
		if strings.HasPrefix(e, "NPM_TOKEN") || strings.HasPrefix(e, "OPXTRA") || strings.HasPrefix(e, "DBUS") || e == "PATH=/evil" {
			t.Fatalf("leaked %q", e)
		}
	}
	st := toolEnv("linux", false)
	if !has(st, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/x") {
		t.Fatalf("secret-tool needs the session bus: %v", st)
	}
	for _, e := range st {
		if strings.HasPrefix(e, "OP_") || strings.HasPrefix(e, "NPM_TOKEN") {
			t.Fatalf("leaked %q", e)
		}
	}
	if m := toolEnv("darwin", false); has(m, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/x") {
		t.Fatal("dbus passed on darwin")
	}
}

func TestRunToolTimeoutAndExit(t *testing.T) {
	p := newHelper(t, helperCfg{Exit: 7, Stdout: "out", Stderr: "err"})
	r := runTool(p, []string{"a", "b"}, "in", []string{"PATH=/usr/bin:/bin"}, shortTimeout)
	if r.exit != 7 || r.stdout != "out" || r.stderr != "err" || r.err != nil {
		t.Fatalf("%+v", r)
	}
	if c := calls(t, p); len(c) != 1 || c[0].Stdin != "in" || strings.Join(c[0].Argv, " ") != "a b" {
		t.Fatalf("%+v", c)
	}
	slow := newHelper(t, helperCfg{SleepMS: 5000})
	r = runTool(slow, nil, "", nil, 200*1000*1000)
	if r.err == nil || !strings.Contains(r.err.Error(), "timed out") {
		t.Fatalf("%+v", r)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("\n\x1b[31m boom \x07\nsecond"); got != "[31m boom" {
		t.Fatalf("%q", got)
	}
}

// A fake tool on PATH is never run: the fixed list is empty here, so the
// reference is unavailable, and the PATH one records nothing.
func TestBackendNeverSearchesPath(t *testing.T) {
	fake := newHelperIn(t, t.TempDir(), "op", helperCfg{Stdout: "stolen"})
	t.Setenv("PATH", filepath.Dir(fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
	b := &Backend{GOOS: "linux", OpPaths: []string{"/nonexistent/op"}}
	v, st, err := b.Resolve("op://Dev/acme-stripe/test-key")
	if v != "" || st != Unavailable || err == nil {
		t.Fatalf("%q %v %v", v, st, err)
	}
	if len(calls(t, fake)) != 0 {
		t.Fatal("ran the op found on PATH")
	}
}
