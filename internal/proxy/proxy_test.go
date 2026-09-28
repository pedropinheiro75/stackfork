package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContainerNameOverride(t *testing.T) {
	if got := ContainerName(); got != "stackfork-proxy" {
		t.Fatalf("expected default stackfork-proxy, got %q", got)
	}
	t.Setenv("STACKFORK_PROXY_NAME", "sf-proxy-test")
	if got := ContainerName(); got != "sf-proxy-test" {
		t.Fatalf("expected override, got %q", got)
	}
}

func TestReadWritePortDefault(t *testing.T) {
	state := t.TempDir()
	port, err := ReadPort(state)
	if err != nil {
		t.Fatal(err)
	}
	if port != DefaultPort {
		t.Fatalf("expected default port %d with nothing persisted, got %d", DefaultPort, port)
	}
	if err := WritePort(state, 18080); err != nil {
		t.Fatal(err)
	}
	port, err = ReadPort(state)
	if err != nil {
		t.Fatal(err)
	}
	if port != 18080 {
		t.Fatalf("expected persisted port 18080, got %d", port)
	}
	fi, err := os.Stat(filepath.Join(state, "proxy", "port"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 port file, got %o", fi.Mode().Perm())
	}
}

// GenerateCaddyfile must never try to get a TLS cert for a .localhost name
// (design.md D7 layer 3): auto_https off in the global block, http://
// (never https://) site addresses.
func TestGenerateCaddyfileNoAutoHTTPS(t *testing.T) {
	out := GenerateCaddyfile([]Route{
		{Hostname: "b.localhost", Target: "host.docker.internal:20002"},
		{Hostname: "a.localhost", Target: "host.docker.internal:20001"},
	})
	if !strings.Contains(out, "auto_https off") {
		t.Fatalf("expected auto_https off in global block:\n%s", out)
	}
	if strings.Contains(out, "https://") {
		t.Fatalf("expected no https:// site address:\n%s", out)
	}
	aIdx := strings.Index(out, "http://a.localhost")
	bIdx := strings.Index(out, "http://b.localhost")
	if aIdx < 0 || bIdx < 0 || aIdx > bIdx {
		t.Fatalf("expected routes sorted by hostname:\n%s", out)
	}
	if !strings.Contains(out, "reverse_proxy host.docker.internal:20001") {
		t.Fatalf("expected a's target in output:\n%s", out)
	}
}

func TestGenerateCaddyfileEmpty(t *testing.T) {
	out := GenerateCaddyfile(nil)
	if !strings.Contains(out, "auto_https off") {
		t.Fatalf("expected global block even with no routes:\n%s", out)
	}
	if strings.Contains(out, "reverse_proxy") {
		t.Fatalf("expected no site blocks:\n%s", out)
	}
}

func TestWriteCaddyfilePrivate(t *testing.T) {
	state := t.TempDir()
	if err := WriteCaddyfile(state, []Route{{Hostname: "a.localhost", Target: "host.docker.internal:20001"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(CaddyfilePath(state))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 Caddyfile, got %o", fi.Mode().Perm())
	}
}

// WriteCaddyfile must write in place (same inode across calls), never
// temp-file+rename: Up() bind-mounts CaddyfilePath as a single file into the
// proxy container, and a rename-based replacement leaves that mount pointing
// at a now-unlinked inode (verified directly against Docker on this
// machine - see WriteCaddyfile's doc comment). This is the regression test
// for that: two writes must land on the same inode.
func TestWriteCaddyfileSameInodeAcrossWrites(t *testing.T) {
	state := t.TempDir()
	if err := WriteCaddyfile(state, []Route{{Hostname: "a.localhost", Target: "host.docker.internal:20001"}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(CaddyfilePath(state))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteCaddyfile(state, []Route{{Hostname: "b.localhost", Target: "host.docker.internal:20002"}}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(CaddyfilePath(state))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("expected WriteCaddyfile to reuse the same inode across writes (in-place), not replace it - a bind-mounted proxy container would lose the file")
	}
}

// WithLock must serialize concurrent callers: two goroutines racing to
// read-modify-write a shared counter inside it must never interleave (task
// 2, design.md D7 layer 3 - the same shape as a route rebuild+write+reload).
func TestWithLockSerializesConcurrentCallers(t *testing.T) {
	state := t.TempDir()
	const n = 20
	counter := 0
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithLock(state, func() error {
				got := counter
				time.Sleep(2 * time.Millisecond) // widen the race window
				counter = got + 1
				return nil
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if counter != n {
		t.Fatalf("expected %d (fully serialized), got %d (a lost update slipped through)", n, counter)
	}
}
