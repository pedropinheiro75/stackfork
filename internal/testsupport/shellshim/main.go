// Command shellshim runs the POSIX shell script that sits next to it without
// the ".exe" suffix (docker.exe runs docker), so a test's #!/bin/sh fake works
// on Windows. testsupport.FakeExe builds it; never shipped.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sh is the POSIX shell (Git for Windows), set with -ldflags -X.
var sh string

func main() {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shellshim:", err)
		os.Exit(127)
	}
	cmd := exec.Command(sh, append([]string{strings.TrimSuffix(self, ".exe")}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// MSYS globs arguments (--profile *) and rewrites ones that look like
	// POSIX paths; a fake must see them as given.
	cmd.Env = append(withShPath(os.Environ()), "MSYS=noglob", "MSYS_NO_PATHCONV=1", "MSYS2_ARG_CONV_EXCL=*")
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "shellshim:", err)
		os.Exit(127)
	}
}

// withShPath puts sh's own directory (cat, sed, env) first on PATH: a store
// tool runs with PATH set to System32 only.
func withShPath(env []string) []string {
	dir := filepath.Dir(sh)
	for i, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
			env[i] = k + "=" + dir + string(os.PathListSeparator) + v
			return env
		}
	}
	return append(env, "PATH="+dir)
}
