//go:build !windows

package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

// A slow dev server is not cut off by the short claim deadline: the port wait
// has its own, longer one (hostClaimWait is shrunk to prove it; no real 10 s).
func TestHostStartWaitsForASlowListener(t *testing.T) {
	old := hostClaimWait
	hostClaimWait = 100 * time.Millisecond
	t.Cleanup(func() { hostClaimWait = old })
	root, stateDir, project := hostEnv(t)
	t.Setenv("STACKFORK_HOST_WAIT_TIMEOUT", "") // the default: wait for the port
	t.Setenv("STACKFORK_TEST_HELPER", "1")
	t.Setenv("HELPER_LISTEN_AFTER", "1200ms")
	g := []string{"--root", root, "--state-dir", stateDir}
	begin := time.Now()
	_, stderr, code := runCLI(t, append(g, "host", "start", "api", "--wait-timeout", "30s", "--", os.Args[0])...)
	if code != 0 || time.Since(begin) < time.Second || time.Since(begin) > 20*time.Second {
		t.Fatalf("start must return once the port answers (after ~1.5 s): code=%d after %v: %s", code, time.Since(begin), stderr)
	}
	proc := readManifestAt(t, stateDir, project).HostProcs["api"]
	if !state.HostIdentity(proc) {
		t.Fatalf("api should be running: %+v", proc)
	}
}

func TestHostStartTimeoutWarningNamesAppPortAndRedactedLogTail(t *testing.T) {
	root, stateDir, project := hostEnv(t)
	t.Setenv("STACKFORK_HOST_WAIT_TIMEOUT", "")
	t.Setenv("STACKFORK_TEST_HELPER", "1")
	t.Setenv("HELPER_LISTEN_AFTER", "1h")
	t.Setenv("HELPER_PRINT", "tok-0123456789abcdef")
	t.Setenv("HOSTWAIT_TOKEN", "tok-0123456789abcdef")
	mp := stateDir + "/" + project + "/manifest.json"
	m := readManifestAt(t, stateDir, project)
	m.Secrets = map[string]string{"api_token": "env:HOSTWAIT_TOKEN"}
	if err := state.WritePrivate(mp, m); err != nil {
		t.Fatal(err)
	}
	g := []string{"--root", root, "--state-dir", stateDir}
	_, stderr, code := runCLI(t, append(g, "host", "start", "api", "--port", "31777", "--wait-timeout", "1s", "--", os.Args[0])...)
	if code != 0 {
		t.Fatalf("a start whose app does not listen yet warns and succeeds: %d %s", code, stderr)
	}
	for _, w := range []string{"api is running but not listening on 31777 after 1s", "booting [redacted:api_token]"} {
		if !strings.Contains(stderr, w) {
			t.Errorf("warning lacks %q:\n%s", w, stderr)
		}
	}
	if strings.Contains(stderr, "tok-0123456789abcdef") {
		t.Errorf("secret leaked:\n%s", stderr)
	}
	if p := readManifestAt(t, stateDir, project).HostProcs["api"]; !state.HostIdentity(p) {
		t.Errorf("the app must keep running: %+v", p)
	}
}

func TestHostLogTailKeepsTheLastLines(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		b.WriteString("line " + strings.Repeat("x", i%3) + string(rune('A'+i%26)) + "\n")
	}
	os.WriteFile(dir+"/host-a.log", []byte(b.String()), 0o600)
	got := strings.Split(strings.TrimRight(hostLogTail(dir, "a", 15), "\n"), "\n")
	if len(got) != 15 || got[14] != strings.Split(strings.TrimRight(b.String(), "\n"), "\n")[39] {
		t.Errorf("tail: %q", got)
	}
}
