package secretstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Fixed tool locations (D2). PATH is never searched.
var (
	securityPaths   = []string{"/usr/bin/security"}
	secretToolPaths = []string{"/usr/bin/secret-tool"}
	opPaths         = []string{"/opt/homebrew/bin/op", "/usr/local/bin/op", "/usr/bin/op"}
)

const (
	shortTimeout = 10 * time.Second
	opTimeout    = 30 * time.Second
)

// resolveTool picks the first existing candidate, follows its symbolic
// links, and refuses it (D2) when the final file or its directory is
// writable by group or others, when either is owned by someone other than
// root or the current user, when it is not an executable regular file, or
// when it sits inside one of the scanned roots. The returned path is the
// resolved one: that is the file to run.
func resolveTool(candidates, scannedRoots []string) (string, error) {
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		p, err := filepath.EvalSymlinks(c)
		if err != nil {
			return "", fmt.Errorf("%s: cannot resolve: %v", c, err)
		}
		if err := checkTool(p, scannedRoots); err != nil {
			return "", fmt.Errorf("refusing %s: %w", p, err)
		}
		return p, nil
	}
	if len(candidates) == 1 {
		return "", fmt.Errorf("%s is not installed", candidates[0])
	}
	return "", fmt.Errorf("not installed (looked in %s)", strings.Join(candidates, ", "))
}

func checkTool(p string, scannedRoots []string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return errors.New("not an executable file")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("the file is writable by group or others")
	}
	dir := filepath.Dir(p)
	di, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if di.Mode().Perm()&0o022 != 0 {
		return errors.New("its directory is writable by group or others")
	}
	if !ownerOK(fi) || !ownerOK(di) {
		return errors.New("its owner is neither root nor the current user")
	}
	for _, r := range scannedRoots {
		if rr, err := filepath.EvalSymlinks(r); err == nil && within(rr, p) {
			return errors.New("it is inside a scanned repository root")
		}
	}
	return nil
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// toolEnv is the minimal environment of a store tool (D2). op also gets the
// OP_* variables (service account, account selection). On Linux the
// Secret Service is reached over the session bus, so the bus and display
// variables pass through too.
func toolEnv(goos string, forOP bool) []string {
	env := []string{"PATH=/usr/bin:/bin"}
	keep := map[string]bool{"HOME": true, "USER": true, "LANG": true}
	if goos == "linux" && !forOP {
		for _, k := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "DISPLAY", "WAYLAND_DISPLAY"} {
			keep[k] = true
		}
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if keep[k] || (forOP && strings.HasPrefix(k, "OP_")) {
			env = append(env, kv)
		}
	}
	return env
}

type toolResult struct {
	stdout, stderr string
	exit           int
	err            error // start failure or timeout; exit is -1 then
}

// runTool runs path with args, stdin and env, under a timeout.
func runTool(path string, args []string, stdin string, env []string, timeout time.Duration) toolResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Dir = string(filepath.Separator)
	cmd.WaitDelay = time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := toolResult{stdout: out.String(), stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		r.exit, r.err = -1, fmt.Errorf("timed out after %s", timeout)
	case errors.As(err, &ee):
		r.exit = ee.ExitCode()
	case err != nil:
		r.exit, r.err = -1, err
	}
	return r
}

// firstLine is the first non-empty line of s with control characters
// removed and a length cap, for an error status.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, l)
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return ""
}
