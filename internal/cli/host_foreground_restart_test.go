//go:build !windows

package cli

import (
	"path/filepath"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// A foreground `host NAME -- cmd` records what `host restart` and `up` need to
// start it again, detached, after its shell is gone.
func TestForegroundHostCanBeRestarted(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	g := []string{"--root", root, "--state-dir", stateDir}
	if _, stderr, code := runCLI(t, append(g, "host", "app", "--", "sh", "-c", "sleep 1")...); code != 0 {
		t.Fatalf("foreground: %d %s", code, stderr)
	}
	mp := filepath.Join(stateDir, project, "manifest.json")
	m := readManifestAt(t, stateDir, project)
	p := m.HostProcs["app"]
	if len(p.Restart) == 0 {
		t.Fatalf("a foreground run must record how to start it again: %+v", p)
	}
	// runCLI runs in this process, which outlives the command; a real foreground stackfork has exited by now
	p.PID, p.StartedAt = 999999, "1970-01-01T00:00:00Z"
	m.HostProcs["app"] = p
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCLI(t, append(g, "host", "restart", "app")...); code != 0 {
		t.Fatalf("restart of a foreground app: %d %s", code, stderr)
	}
	np := readManifestAt(t, stateDir, project).HostProcs["app"]
	if np.PID == 999999 || !state.HostIdentity(np) || !contains(np.Cmdline, "--supervise") {
		t.Fatalf("restart should leave a running supervised app: %+v", np)
	}
}
