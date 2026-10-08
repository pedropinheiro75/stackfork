package repos

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func init() {
	// Fixture repos must never trigger a signing prompt.
	for k, v := range map[string]string{
		"GIT_CONFIG_COUNT": "2", "GIT_CONFIG_KEY_0": "commit.gpgsign", "GIT_CONFIG_VALUE_0": "false",
		"GIT_CONFIG_KEY_1": "tag.gpgsign", "GIT_CONFIG_VALUE_1": "false",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		os.Setenv(k, v)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// mkRepo creates a scratch repo with one commit on main and returns its
// resolved path.
func mkRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-q", "-m", "init")
	r, _ := filepath.EvalSymlinks(dir)
	return r
}

func tempRoot(t *testing.T) string {
	t.Helper()
	r, _ := filepath.EvalSymlinks(t.TempDir())
	return r
}

// installGitLog puts a `git` wrapper first on PATH that logs every call
// (one line, args space-joined) and then runs the real git.
func installGitLog(t *testing.T) (logPath string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "log.txt")
	script := "#!/bin/sh\n{ for a in \"$@\"; do printf '%s ' \"$a\"; done; printf '\\n'; } >> \"" + logPath + "\"\nexec \"" + real + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readLog(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}
