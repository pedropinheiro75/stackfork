package cli

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/state"
)

func writeHostManifest(t *testing.T, base string, proc state.HostProc) {
	t.Helper()
	dir := filepath.Join(base, "sf-web-abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(state.Manifest{HostProcs: map[string]state.HostProc{"web": proc}})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func deadPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Skip("no true binary")
	}
	return c.Process.Pid
}

func TestDoctorOrphanedHostApp(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	dead := state.HostProc{PID: deadPID(t), StartedAt: "1970-01-01T00:00:00Z", Port: port}

	base := t.TempDir()
	writeHostManifest(t, base, dead)
	got := orphanedHostApps(base)
	if len(got) != 1 || !strings.Contains(got[0], "web") || !strings.Contains(got[0], "port") {
		t.Fatalf("answering port: %v", got)
	}

	// Closed port: a normal exited row, not an orphan.
	l.Close()
	if got := orphanedHostApps(base); len(got) != 0 {
		t.Fatalf("closed port: %v", got)
	}
}

func TestDoctorLiveHostAppIsNotOrphan(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	start, err := state.ProcStart(os.Getpid())
	if err != nil {
		t.Skip(err)
	}
	base := t.TempDir()
	writeHostManifest(t, base, state.HostProc{PID: os.Getpid(), StartedAt: start, Port: port})
	if got := orphanedHostApps(base); len(got) != 0 {
		t.Fatalf("live supervisor: %v", got)
	}
}
