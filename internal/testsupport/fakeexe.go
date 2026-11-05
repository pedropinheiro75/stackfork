package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// FakeExe writes a #!/bin/sh fake tool at path and returns the path to run
// it by. On Unix that is path itself. On Windows, which cannot run a script,
// it also puts a shellshim at path+".exe" (found by PATH lookup) that runs the
// script with Git for Windows' sh, and returns that; without a sh the test
// skips.
func FakeExe(t *testing.T, path, script string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		return path
	}
	shim := buildShim(t)
	exe := path + ".exe"
	data, err := os.ReadFile(shim)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

var shim struct {
	once sync.Once
	path string
	skip string
	err  string
}

func buildShim(t *testing.T) string {
	t.Helper()
	shim.once.Do(func() {
		sh, err := exec.LookPath("sh")
		if err != nil {
			sh = `C:\Program Files\Git\usr\bin\sh.exe`
			if _, err := os.Stat(sh); err != nil {
				shim.skip = "no POSIX sh (Git for Windows) to run the fake tools"
				return
			}
		}
		dir, err := os.MkdirTemp("", "stackfork-shellshim-")
		if err != nil {
			shim.err = err.Error()
			return
		}
		out := filepath.Join(dir, "shellshim.exe")
		cmd := exec.Command("go", "build", "-o", out, "-ldflags", "-X 'main.sh="+sh+"'",
			"github.com/pedropinheiro75/stackfork/internal/testsupport/shellshim")
		if b, err := cmd.CombinedOutput(); err != nil {
			shim.err = "building shellshim: " + err.Error() + ": " + string(b)
			return
		}
		shim.path = out
	})
	if shim.err != "" {
		t.Fatal(shim.err)
	}
	if shim.path == "" {
		t.Skip(shim.skip)
	}
	return shim.path
}
