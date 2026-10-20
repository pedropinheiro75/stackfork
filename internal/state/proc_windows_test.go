//go:build windows

package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestProcStartSelf(t *testing.T) {
	got, err := ProcStart(os.Getpid())
	if err != nil || !strings.HasPrefix(got, "filetime:") {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestExitedChildNotAlive(t *testing.T) {
	c := exec.Command("ping", "-n", "30", "127.0.0.1")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	start, err := ProcStart(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !processAlive(c.Process.Pid, start) {
		t.Fatal("running child must be alive")
	}
	c.Process.Kill()
	c.Wait()
	if processAlive(c.Process.Pid, start) {
		t.Fatal("exited child reported alive")
	}
}

func TestDetachAttrHasHiddenConsole(t *testing.T) {
	a := DetachAttr()
	if a.CreationFlags&createNoWindow == 0 || a.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("flags %#x", a.CreationFlags)
	}
}

func TestKillHostChecksIdentity(t *testing.T) {
	c := exec.Command("ping", "-n", "30", "127.0.0.1")
	c.SysProcAttr = DetachAttr()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { c.Process.Kill(); c.Wait() }()
	start, err := ProcStart(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if KillHost(HostProc{PID: c.Process.Pid, StartedAt: "filetime:1"}, 0) == nil {
		t.Fatal("a different creation time must not be killed")
	}
	if !processAlive(c.Process.Pid, start) {
		t.Fatal("child must still run")
	}
	if err := KillHost(HostProc{PID: c.Process.Pid, StartedAt: start}, 0); err != nil {
		t.Fatal(err)
	}
	c.Wait()
	if processAlive(c.Process.Pid, start) {
		t.Fatal("child still alive")
	}
}

func TestKillGroupKillsChild(t *testing.T) {
	c := exec.Command("ping", "-n", "30", "127.0.0.1")
	c.SysProcAttr = DetachAttr()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	start, err := ProcStart(c.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := KillGroup(c.Process.Pid, 0); err != nil {
		t.Fatal(err)
	}
	c.Wait()
	if processAlive(c.Process.Pid, start) {
		t.Fatal("child still alive")
	}
	if KillGroup(os.Getpid(), 0) == nil || KillGroup(4, 0) == nil {
		t.Fatal("must refuse self and pid 4")
	}
}

func TestAcquireLockSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	var mu sync.Mutex
	held, overlap := 0, false
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := AcquireLock(path)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			held++
			if held > 1 {
				overlap = true
			}
			mu.Unlock()
			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			held--
			mu.Unlock()
			l.Release()
		}()
	}
	wg.Wait()
	if overlap {
		t.Fatal("locks overlapped")
	}
}
